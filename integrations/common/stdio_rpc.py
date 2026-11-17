"""Newline JSON-RPC for a private child process; no listening network socket."""
import asyncio
import json
import uuid


class RPC:
    def __init__(self, argv, notify):
        self.argv, self.notify, self.pending = argv, notify, {}

    async def start(self):
        self.proc = await asyncio.create_subprocess_exec(*self.argv, stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, limit=8 << 20)
        self.reader = asyncio.create_task(self.read())

    async def read(self):
        try:
            while line := await self.proc.stdout.readline():
                event = json.loads(line)
                if 'id' not in event:
                    self.notify(event)
                    continue
                future = self.pending.get(str(event['id']))
                if future and not future.done():
                    if 'error' in event:
                        future.set_exception(RuntimeError('Provider rejected operation'))
                    else:
                        future.set_result(event.get('result'))
            raise ConnectionError('Provider process stopped')
        finally:
            for future in self.pending.values():
                if not future.done():
                    future.set_exception(ConnectionError('Provider process stopped'))

    async def call(self, method, params=None):
        if self.reader.done():
            self.reader.result()
        request_id = str(uuid.uuid4())
        future = asyncio.get_running_loop().create_future()
        self.pending[request_id] = future
        try:
            self.proc.stdin.write((json.dumps({'jsonrpc':'2.0','id':request_id,'method':method,'params':params or {}})+'\n').encode())
            await self.proc.stdin.drain()
            return await asyncio.wait_for(future,90)
        finally:
            self.pending.pop(request_id,None)

    async def close(self):
        if self.proc.returncode is None:
            self.proc.terminate()
        try:
            await asyncio.wait_for(self.proc.wait(),5)
        except asyncio.TimeoutError:
            self.proc.kill()
            await self.proc.wait()
        self.reader.cancel()
        await asyncio.gather(self.reader,return_exceptions=True)
