"""Gmail and Microsoft Graph adapters; every reply is pinned to a native parent."""
import asyncio
import base64
import datetime as dt
from email import policy
from email.message import EmailMessage
from email.parser import BytesParser
from email.utils import getaddresses, make_msgid
from email.headerregistry import Address
import hashlib
from html.parser import HTMLParser
import json
import urllib.parse
from oauth import request, HTTPFailure

UTC = dt.timezone.utc

def clipped(text, limit): return str(text).encode()[:limit].decode('utf-8','ignore')
def digest(value): return hashlib.sha256(value.encode()).hexdigest()
def decode64(value): return base64.urlsafe_b64decode(value+'='*(-len(value)%4))
def quote(value): return urllib.parse.quote(value,safe='')

class TextHTML(HTMLParser):
    def __init__(self): super().__init__(); self.parts=[]; self.skip=0
    def handle_starttag(self, tag, attrs):
        if tag in ('script','style','head'): self.skip+=1
        if not self.skip and tag in ('br','p','div','li','tr'): self.parts.append('\n')
    def handle_endtag(self, tag):
        if tag in ('script','style','head') and self.skip: self.skip-=1
    def handle_data(self, data):
        if not self.skip: self.parts.append(data)

def html_text(value):
    parser=TextHTML();parser.feed(value);return ''.join(parser.parts).strip()

def mime_text(message):
    part=message.get_body(preferencelist=('plain','html'))
    body=''
    if part:
        raw=part.get_payload(decode=True) or b''
        try: body=raw.decode(part.get_content_charset() or 'utf-8',errors='replace')
        except LookupError: body=raw.decode('utf-8',errors='replace')
        if part.get_content_type()=='text/html': body=html_text(body)
    for attachment in message.iter_attachments():
        body+='\n[Email attachment: '+str(attachment.get_filename() or 'file')+' · open in mail app]'
    return body or '[Empty email]'

def conversation(provider, account, chat, subject):
    return {'id':provider+'/'+digest(account+'\0'+chat),'service':{'gmail':'Gmail','outlook':'Outlook'}[provider],
        'account':account,'name':clipped(subject or '(No subject)',300),'recipient':chat,'demo':False,
        'route':{'provider':provider,'account':account,'chat':chat,'thread':''}}

def reply_mime(account, original, body, files):
    recipients=getaddresses([str(original.get('Reply-To') or original.get('From') or '')])
    if not recipients or len(recipients)>10: raise ValueError('Original email has no usable reply address')
    addresses=[]
    for name,address in recipients:
        parsed=Address(display_name=name,addr_spec=address)
        if not parsed.domain: raise ValueError('Original email has no usable reply address')
        addresses.append(parsed)
    parent=str(original.get('Message-ID') or '').strip()
    if not parent or '\n' in parent or '\r' in parent or not (parent.startswith('<') and parent.endswith('>')):
        raise ValueError('Original email has no usable Message-ID')
    message=EmailMessage(policy=policy.SMTP)
    message['From']=account;message['To']=addresses
    subject=str(original.get('Subject') or '')
    message['Subject']=subject if subject.lower().startswith('re:') else 'Re: '+subject
    message['In-Reply-To']=parent
    refs=' '.join(str(original.get('References') or '').split())
    message['References']=(refs+' '+parent).strip()[-8000:]
    message['Message-ID']=make_msgid(domain='amythest.invalid')
    message.set_content(body)
    for attachment,data in files:
        mime=attachment.get('mime','application/octet-stream').split('/')
        if len(mime)!=2 or any(not p or not all(c.isalnum() or c in '.+-' for c in p) for p in mime): mime=['application','octet-stream']
        message.add_attachment(data,maintype=mime[0],subtype=mime[1],filename=attachment['name'])
    return message.as_bytes()

class Provider:
    def __init__(self, oauth, journal, cfg): self.oauth,self.journal,self.cfg=oauth,journal,cfg;self.account=''
    async def call(self, path, data=None, headers=None):
        url=self.base+path if path.startswith('/') else path
        # Pagination links are data, not permission to send a bearer elsewhere.
        if not url.startswith(self.base+'/') or urllib.parse.urlsplit(url).fragment:
            raise ValueError('Provider pagination URL escaped its API')
        def perform():
            fields=dict(self.headers);fields.update(headers or {});fields['Authorization']='Bearer '+self.oauth.access_token()
            return request(url,data,fields)
        task=asyncio.create_task(asyncio.to_thread(perform))
        try: return await asyncio.shield(task)
        except asyncio.CancelledError:
            # Do not reopen the account while its old refresh-token write or
            # provider POST is still running in a worker thread.
            try: await task
            except Exception: pass
            raise
    def get(self,key):
        row=self.journal.db.execute('SELECT value FROM metadata WHERE key=?',(key,)).fetchone()
        return json.loads(row[0]) if row else None
    def put(self,key,value):
        with self.journal.db:
            self.journal.db.execute('INSERT INTO metadata VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value',(key,json.dumps(value)))
    def remember(self, native_id, raw_id): self.put('native/'+native_id,raw_id)
    def capture(self, message):
        value=self.normalize(message)
        if value: self.journal.spool(value)

class Gmail(Provider):
    base='https://gmail.googleapis.com/gmail/v1/users/me'
    headers={}
    async def identity(self):
        self.account=(await self.call('/profile'))['emailAddress'].lower();return self.account
    def normalize(self, item):
        labels=set(item.get('labelIds',[]))
        if labels & {'SPAM','TRASH','DRAFT'}: return None
        message=BytesParser(policy=policy.default).parsebytes(decode64(item['raw']))
        return {'conversation':conversation('gmail',self.account,item['threadId'],str(message.get('Subject',''))),
            'message':{'native_id':item['id'],'sender':clipped(message.get('From','Email'),200),'body':clipped(mime_text(message),20000),
                'outgoing':'SENT' in labels,'at':dt.datetime.fromtimestamp(int(item['internalDate'])/1000,UTC).isoformat()}}
    async def fetch(self, native_id): return await self.call('/messages/'+quote(native_id)+'?format=raw')
    async def sync(self):
        cursor=self.get('gmail/history')
        if cursor:
            path='/history?'+urllib.parse.urlencode({'startHistoryId':cursor,'historyTypes':'messageAdded','maxResults':500})
            try:
                while path:
                    page=await self.call(path)
                    for history in page.get('history',[]):
                        for added in history.get('messagesAdded',[]):
                            try: self.capture(await self.fetch(added['message']['id']))
                            except HTTPFailure as error:
                                if error.status!=404: raise
                    next_token=page.get('nextPageToken')
                    path='/history?'+urllib.parse.urlencode({'startHistoryId':cursor,'historyTypes':'messageAdded','maxResults':500,'pageToken':next_token}) if next_token else ''
                self.put('gmail/history',page['historyId']);return
            except HTTPFailure as error:
                if error.status!=404: raise
                # History has expired: capture a fresh bounded window before
                # installing its pre-snapshot cursor, so concurrent arrivals replay.
        high=(await self.call('/profile'))['historyId']
        remaining=self.cfg['initial_limit'];token=''
        while remaining:
            query={'maxResults':min(remaining,500),'q':'newer_than:'+str(self.cfg['initial_days'])+'d -in:spam -in:trash -in:drafts'}
            if token: query['pageToken']=token
            page=await self.call('/messages?'+urllib.parse.urlencode(query))
            for item in page.get('messages',[]):
                try: self.capture(await self.fetch(item['id']))
                except HTTPFailure as error:
                    if error.status!=404: raise
                remaining-=1
            token=page.get('nextPageToken','')
            if not token or not page.get('messages'): break
        self.put('gmail/history',high)
    async def prepare(self, delivery, files):
        parent=await self.fetch(delivery['parent']['native_id'])
        target=delivery['draft']['target']['route']
        if parent['threadId']!=target['chat'] or 'SENT' in parent.get('labelIds',[]):
            raise ValueError('Original email does not match this reply destination')
        message=BytesParser(policy=policy.default).parsebytes(decode64(parent['raw']))
        raw=reply_mime(self.account,message,delivery['draft']['body'],files)
        return {'threadId':parent['threadId'],'raw':base64.urlsafe_b64encode(raw).decode()}
    async def send(self, delivery, prepared):
        result=await self.call('/messages/send',prepared)
        if not result.get('id') or result.get('threadId')!=prepared['threadId']:
            raise ValueError('Provider did not confirm the reply thread')
        return 'sent',result['id']

class Outlook(Provider):
    base='https://graph.microsoft.com/v1.0'
    headers={'Prefer':'IdType="ImmutableId", outlook.body-content-type="text"'}
    fields='id,conversationId,subject,from,replyTo,toRecipients,body,receivedDateTime,sentDateTime,isDraft,parentFolderId,hasAttachments,internetMessageId,internetMessageHeaders'
    async def identity(self):
        profile=await self.call('/me?$select=id,mail,userPrincipalName')
        identity=profile['id']
        previous=self.get('outlook/identity')
        if previous and previous!=identity: raise ValueError('OAuth login changed mailbox identity; use separate state')
        self.put('outlook/identity',identity)
        self.account=(profile.get('mail') or profile['userPrincipalName']).lower()
        self.sent=(await self.call('/me/mailFolders/sentitems?$select=id'))['id']
        return self.account
    def normalize(self, item):
        if item.get('@removed') or item.get('isDraft'): return None
        native=digest(item['id']);chat=digest(item['conversationId']);self.remember(native,item['id'])
        sender=item.get('from',{}).get('emailAddress',{})
        body=item.get('body',{});text=body.get('content','')
        if body.get('contentType','').lower()=='html': text=html_text(text)
        if item.get('hasAttachments'): text+='\n[Email attachments · open in Outlook]'
        outgoing=item.get('parentFolderId')==self.sent or sender.get('address','').lower()==self.account
        return {'conversation':conversation('outlook',self.account,chat,item.get('subject','')),
            'message':{'native_id':native,'sender':clipped((sender.get('name','')+' <'+sender.get('address','')+'>').strip(),200),
                'body':clipped(text or '[Empty email]',20000),'outgoing':outgoing,
                'at':item.get('sentDateTime') if outgoing else item['receivedDateTime']}}
    async def folders(self):
        if self.cfg['folders']: return self.cfg['folders']
        excluded=set()
        for name in ('deleteditems','junkemail'):
            excluded.add((await self.call('/me/mailFolders/'+name+'?$select=id'))['id'])
        pending=['/me/mailFolders?$top=100&$select=id,childFolderCount'];ids=[]
        while pending:
            page=await self.call(pending.pop())
            if page.get('@odata.nextLink'): pending.append(page['@odata.nextLink'])
            for folder in page.get('value',[]):
                if folder['id'] in excluded: continue
                ids.append(folder['id'])
                if folder.get('childFolderCount'): pending.append('/me/mailFolders/'+quote(folder['id'])+'/childFolders?$top=100&$select=id,childFolderCount')
                if len(ids)>2000: raise ValueError('Mailbox has too many folders; configure a folder subset')
        return ids
    async def sync(self):
        for folder in await self.folders():
            key='outlook/delta/'+folder
            path=self.get(key)
            if not path:
                since=(dt.datetime.now(UTC)-dt.timedelta(days=self.cfg['initial_days'])).isoformat().replace('+00:00','Z')
                path='/me/mailFolders/'+quote(folder)+'/messages/delta?'+urllib.parse.urlencode({'$select':self.fields,'$filter':'receivedDateTime ge '+since})
            while path:
                try: page=await self.call(path)
                except HTTPFailure as error:
                    if error.status in (404,410): self.put(key,None);break
                    raise
                for item in page.get('value',[]): self.capture(item)
                # Each page is durable before its next/delta link. On a crash,
                # re-reading the last page is harmless at the central gateway.
                next_link=page.get('@odata.nextLink')
                cursor=next_link or page.get('@odata.deltaLink')
                if not cursor: raise ValueError('Outlook omitted its sync cursor')
                self.put(key,cursor);path=next_link
    async def prepare(self, delivery, files):
        native=delivery['parent']['native_id'];raw_id=self.get('native/'+native)
        if not raw_id or digest(raw_id)!=native: raise ValueError('Original Outlook message is not in this connector journal')
        parent=await self.call('/me/messages/'+quote(raw_id)+'?$select='+self.fields)
        if digest(parent['conversationId'])!=delivery['draft']['target']['route']['chat'] or parent.get('isDraft'):
            raise ValueError('Original Outlook conversation changed')
        if parent.get('parentFolderId')==self.sent or parent.get('from',{}).get('emailAddress',{}).get('address','').lower()==self.account:
            raise ValueError('Select an incoming email to reply')
        original=EmailMessage()
        original['Message-ID']=parent['internetMessageId'];original['Subject']=parent.get('subject','')
        recipients=parent.get('replyTo') or [parent['from']]
        original['Reply-To']=[Address(display_name=r['emailAddress'].get('name',''),addr_spec=r['emailAddress']['address']) for r in recipients]
        for header in parent.get('internetMessageHeaders',[]):
            if header['name'].lower()=='references': original['References']=header['value'];break
        if sum(len(data) for _,data in files)>2<<20: raise ValueError('Outlook attachments exceed 2 MiB')
        raw=reply_mime(self.account,original,delivery['draft']['body'],files)
        return raw_id,base64.b64encode(raw)
    async def send(self, delivery, prepared):
        raw_id,body=prepared
        await self.call('/me/messages/'+quote(raw_id)+'/reply',body,{'Content-Type':'text/plain'})
        return 'submitted',''
