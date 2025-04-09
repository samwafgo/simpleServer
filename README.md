# simpleServer

Test SamWaf loadBalance

usage:
simpleServer.exe port

example:

./simpleServer.exe 6001 6002

```
curl 127.0.0.1:6001
{"port":"6001"}
```

./simpleServer.exe longtime 6001 6002

longtime : sleep 300s with per request


websocket:

./simpleServer.exe longtime 6001 6002 ws

 