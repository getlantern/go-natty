go-natty provides a Go wrapper around the
[natty](https://github.com/getlantern/natty) NAT-traversal utility.

To install:

`go get github.com/getlantern/go-natty`

For docs:

[`godoc github.com/getlantern/go-natty/natty`](https://godoc.org/github.com/getlantern/go-natty/natty)

## Demo

There's a [demo application](tree/master/demo) available.  Right now it only
works on OS X.  Binaries are available
[here](https://github.com/getlantern/go-natty/releases/download/demo-0.0.1/natty-demo-osx).

The demo application allows running a client as well as a server peer.  The
client will connect to the server and obtain its IP address by making a request
to [ifconfig.me](http://ifconfig.me/ip).

The client and server signal with each other using
[waddell](getlantern/waddell) and use [go-udtrelay](getlantern/go-udtrelay) for
a reliable udp-based transport.  The client finds the server on waddell using
its waddell id.

### Example Demo Session

#### Server

```bash
Macintosh% ./natty-demo-osx -mode server        
2014/09/16 16:21:06 Starting server, waddell id is "166bd5f6-700d-4337-8bfc-969cb4bd00e3"
2014/09/16 16:22:01 Got five tuple: &{udp 192.168.1.160:52479 192.168.1.160:61790}
```

Note - you have to specify the waddell id emitted by the server when running the
client.

#### Client

```bash
Macintosh% ./natty-demo-osx -mode client -server "166bd5f6-700d-4337-8bfc-969cb4bd00e3"
2014/09/16 16:21:58 Starting client, connecting to server 166bd5f6-700d-4337-8bfc-969cb4bd00e3 ...
2014/09/16 16:22:01 Got five tuple: &{udp 192.168.1.160:61790 192.168.1.160:52479}
2014/09/16 16:22:08 Proxy's IP is: 66.69.242.177
Macintosh% 
```

Acknowledgements:

go-natty is just a wrapper around [natty](https://github.com/getlantern/natty),
which is itself just a wrapper around the
[WebRTC Native Code Package](http://www.webrtc.org/webrtc-native-code-package).