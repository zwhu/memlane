"""No SDK/network/model: actual adapter consumer with injected HTTP transport."""
import asyncio
import importlib.util
import json
import pathlib
import sys
import types
import traceback
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('adapter', pathlib.Path(__file__).with_name('cognee_processing_adapter.py'))
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)
SECRET = 'SECRET http://127.0.0.1:9876/private?token=LEAK provider schema body'

class TransportError(Exception): pass
class TimeoutException(TransportError): pass
class ConnectTimeout(TimeoutException): pass
class ReadTimeout(TimeoutException): pass
class ConnectError(TransportError): pass
class Model:
 @staticmethod
 def model_json_schema(): return {'type': 'object'}
 @staticmethod
 def model_validate_json(raw):
  if raw == 'bad': raise ValueError(SECRET)
  return json.loads(raw)

class Tests(unittest.TestCase):
 def run_adapter(self, status=200, body=None, error=None):
  class Response:
   status_code = status
   async def __aenter__(self):
    if error: raise error
    return self
   async def __aexit__(self,*args): return False
   async def aiter_bytes(self):
    yield body if body is not None else json.dumps({'choices':[{'finish_reason':'stop','message':{'content':'{}'}}]}).encode()
  class Client:
   def __init__(self,**kwargs):
    assert kwargs == {'trust_env':False,'follow_redirects':False,'timeout':(120,5)}
   async def __aenter__(self): return self
   async def __aexit__(self,*args): return False
   def stream(self,*args,**kwargs):
    payload=json.loads(kwargs['content'])
    assert payload['max_completion_tokens']==8192 and payload['stream'] is False
    assert payload['response_format']['type']=='json_schema'
    assert kwargs['headers']['Authorization']=='Bearer SECRET'
    return Response()
  module=types.SimpleNamespace(AsyncClient=Client,Timeout=lambda value,connect:(value,connect),TransportError=TransportError,TimeoutException=TimeoutException,ConnectTimeout=ConnectTimeout,ReadTimeout=ReadTimeout,ConnectError=ConnectError)
  with patch.dict(sys.modules,{'httpx':module}):
   return asyncio.run(adapter.ProcessingAdapter('http://127.0.0.1:9876/v1','SECRET').acreate_structured_output('input','system',Model))
 def check(self,code,**kwargs):
  with self.assertRaises(ValueError) as caught: self.run_adapter(**kwargs)
  self.assertEqual(str(caught.exception),code)
  self.assertNotIn('SECRET',repr(caught.exception))
  self.assertNotIn('127.0.0.1',repr(caught.exception))
  self.assertTrue(caught.exception.__suppress_context__)
  self.assertNotIn(SECRET,''.join(traceback.format_exception(type(caught.exception), caught.exception, caught.exception.__traceback__)))
 def test_http409(self): self.check('material_model_http_409',status=409,body=SECRET.encode())
 def test_transport(self): self.check('material_model_transport_connect',error=ConnectError(SECRET))
 def test_timeout(self): self.check('material_model_timeout_read',error=ReadTimeout(SECRET))
 def test_json(self): self.check('material_model_response_json_invalid',body=SECRET.encode())
 def test_schema(self): self.check('material_model_response_schema_invalid',body=b'{"choices":[{"finish_reason":"stop","message":{"content":"bad"}}]}')
 def test_envelope(self): self.check('material_model_response_invalid',body=b'[]')
 def test_unknown_status(self): self.check('material_model_http_unknown',status=599,body=SECRET.encode())
 def test_incomplete(self): self.check('material_model_incomplete',body=b'{"choices":[{"finish_reason":"length"}]}')
 def test_unknown(self): self.check('material_model_unknown',error=RuntimeError(SECRET))
 def test_response_bound(self): self.check('material_model_response_unbounded',body=b'x'*(2*1024*1024+1))
 def test_valid(self): self.assertEqual(self.run_adapter(),{})
if __name__ == '__main__': unittest.main()
