"""Private OAuth files and bounded HTTPS requests. Never log provider bodies."""
import base64
import hashlib
import http.server
import json
import os
from pathlib import Path
import secrets
import ssl
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import webbrowser

GOOGLE_TOKEN = 'https://oauth2.googleapis.com/token'
GMAIL_SCOPES = 'https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.send'
GRAPH_SCOPES = 'offline_access https://graph.microsoft.com/User.Read https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/Mail.Send'

class HTTPFailure(Exception):
    def __init__(self, status, code=''):
        self.status, self.code = status, code
        super().__init__('Provider HTTP ' + str(status))

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs): return None

def request(url, data=None, headers=None, form=False):
    if urllib.parse.urlsplit(url).scheme != 'https':
        raise ValueError('Provider requests require HTTPS')
    context = ssl.create_default_context(cafile='/etc/ssl/cert.pem' if Path('/etc/ssl/cert.pem').exists() else None)
    opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPSHandler(context=context))
    headers = dict(headers or {})
    if data is not None:
        if form:
            data = urllib.parse.urlencode(data).encode()
            headers['Content-Type'] = 'application/x-www-form-urlencoded'
        elif not isinstance(data, bytes):
            data = json.dumps(data).encode()
            headers['Content-Type'] = 'application/json'
    req = urllib.request.Request(url, data=data, headers=headers)
    try:
        with opener.open(req, timeout=30) as response:
            raw = response.read((32 << 20) + 1)
            if len(raw) > 32 << 20: raise ValueError('Provider response exceeds size limit')
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        # Error descriptions can include mailbox contents or credentials.
        code = ''
        try:
            value = json.loads(error.read(65536)).get('error')
            if isinstance(value, str) and value in ('authorization_pending','slow_down','access_denied','expired_token','invalid_grant'):
                code = value
        except (ValueError, AttributeError): pass
        raise HTTPFailure(error.code, code) from None

def private_write(path, value):
    path = Path(path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = path.with_name(path.name + '.' + secrets.token_hex(8) + '.tmp')
    try:
        with open(temporary, 'x', opener=lambda p, flags: os.open(p, flags, 0o600)) as stream:
            json.dump(value, stream); stream.write('\n'); stream.flush(); os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)

def microsoft_base(tenant):
    # Tenant is an identifier, never a URL or path supplied by configuration.
    if not tenant or any(c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-' for c in tenant):
        raise ValueError('Invalid Microsoft tenant')
    return 'https://login.microsoftonline.com/' + tenant + '/oauth2/v2.0'

class OAuth:
    def __init__(self, path, provider):
        self.path = Path(path).expanduser()
        if self.path.stat().st_mode & 0o077:
            raise ValueError('OAuth file must be private (chmod 600)')
        self.value = json.loads(self.path.read_text())
        self.lock = threading.Lock()
        if self.value.get('provider') != provider:
            raise ValueError('OAuth file belongs to another provider')
        self.endpoint = GOOGLE_TOKEN if provider == 'gmail' else microsoft_base(self.value.get('tenant','organizations')) + '/token'

    def access_token(self):
        with self.lock:
            if self.value.get('expires_at', 0) <= time.time() + 90:
                data = {'grant_type':'refresh_token', 'refresh_token':self.value['refresh_token'], 'client_id':self.value['client_id']}
                if self.value.get('client_secret'): data['client_secret'] = self.value['client_secret']
                result = request(self.endpoint, data, form=True)
                self.value.update(result)
                self.value['expires_at'] = time.time() + result['expires_in']
                private_write(self.path, self.value)
            return self.value['access_token']

def google_login(credentials_path, output, open_browser=True):
    credentials = json.loads(Path(credentials_path).expanduser().read_text())
    credentials = credentials.get('installed', credentials)
    if not credentials.get('client_id') or not credentials.get('client_secret'):
        raise ValueError('Use Google OAuth credentials for a Desktop application')
    state, verifier = secrets.token_urlsafe(32), secrets.token_urlsafe(64)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip('=')
    result = {}
    class Callback(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            values = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
            valid = values.get('state') == [state]
            self.send_response(200 if valid else 400); self.end_headers()
            if valid:
                result.update(values)
                self.wfile.write(b'Sign-in received. Return to your terminal.')
            else: self.wfile.write(b'Invalid sign-in state.')
        def log_message(self, *args): pass
    with http.server.HTTPServer(('127.0.0.1',0),Callback) as server:
        server.timeout = 1
        redirect = 'http://127.0.0.1:' + str(server.server_port)
        url = 'https://accounts.google.com/o/oauth2/v2/auth?' + urllib.parse.urlencode({
            'client_id':credentials['client_id'],'redirect_uri':redirect,'response_type':'code',
            'scope':GMAIL_SCOPES,'state':state,'code_challenge':challenge,'code_challenge_method':'S256',
            'access_type':'offline','prompt':'consent select_account'})
        print('Sign in on this computer (the callback is loopback-only):\n' + url, flush=True)
        if open_browser: webbrowser.open(url)
        deadline = time.monotonic() + 600
        while not result and time.monotonic() < deadline: server.handle_request()
    if not result.get('code'): raise ValueError('Google sign-in cancelled or expired')
    token = request(GOOGLE_TOKEN, {'grant_type':'authorization_code','code':result['code'][0],
        'client_id':credentials['client_id'],'client_secret':credentials['client_secret'],
        'redirect_uri':redirect,'code_verifier':verifier}, form=True)
    if not token.get('refresh_token'): raise ValueError('Google did not grant offline access')
    token.update(provider='gmail',client_id=credentials['client_id'],client_secret=credentials['client_secret'],expires_at=time.time()+token['expires_in'])
    private_write(output,token)

def microsoft_login(client_id, tenant, output):
    base = microsoft_base(tenant)
    flow = request(base+'/devicecode',{'client_id':client_id,'scope':GRAPH_SCOPES},form=True)
    print('Open '+flow['verification_uri']+' and enter '+flow['user_code'],flush=True)
    deadline = time.monotonic()+flow['expires_in']; interval=max(1,flow.get('interval',5))
    while time.monotonic()<deadline:
        time.sleep(interval)
        try:
            token=request(base+'/token',{'grant_type':'urn:ietf:params:oauth:grant-type:device_code',
                'client_id':client_id,'device_code':flow['device_code']},form=True)
            if not token.get('refresh_token'): raise ValueError('Microsoft did not grant offline access')
            token.update(provider='outlook',client_id=client_id,tenant=tenant,expires_at=time.time()+token['expires_in'])
            private_write(output,token);return
        except HTTPFailure as error:
            if error.code=='authorization_pending': continue
            if error.code=='slow_down': interval+=5;continue
            raise ValueError('Microsoft sign-in was denied or requires organization approval') from None
    raise ValueError('Microsoft sign-in expired')
