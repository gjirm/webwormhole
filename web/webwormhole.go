// +build js,wasm

// WebAssembly program webwormhole is a set of wrappers for webwormhole and
// related packages in order to run in browser.
//
// All functions are added to the webwormhole global object.
package main

import (
	"log"
	"strings"
	"syscall/js"

	webrtc "github.com/pion/webrtc/v2"
	"rsc.io/qr"
	"webwormhole.io/wordlist"
	"webwormhole.io/wormhole"
)

func promise(f func(resolve, reject js.Value)) interface{} {
	return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		go f(args[0], args[1])
		return nil
	}))
}

// conn is the connection we're trying to make. We only support one for now.
var conn *wormhole.Wormhole

// peerconn is the underlying PeerConnection object. We only support one for now.
var peerconn *webrtc.PeerConnection

// qrencode(url string) (png []byte)
func qrencode(_ js.Value, args []js.Value) interface{} {
	code, err := qr.Encode(args[0].String(), qr.L)
	if err != nil {
		return nil
	}
	png := code.PNG()
	dst := js.Global().Get("Uint8Array").New(len(png))
	js.CopyBytesToJS(dst, png)
	return dst
}

func newwormhole(_ js.Value, args []js.Value) interface{} {
	sigserv := args[0].String()
	return promise(func(resolve, reject js.Value) {
		var err error
		conn, err = wormhole.New(sigserv)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		peerconn, err = webrtc.NewPeerConnection(webrtc.Configuration{
			ICEServers: conn.ICEServers,
		})
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		resolve.Invoke([]interface{}{
			conn.Slot,
			peerconn,
		})
		return
	})
}

func joinwormhole(_ js.Value, args []js.Value) interface{} {
	sigserv := args[0].String()
	slot := args[1].String()
	return promise(func(resolve, reject js.Value) {
		var err error
		conn, err = wormhole.Join(sigserv, slot)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		peerconn, err = webrtc.NewPeerConnection(webrtc.Configuration{
			ICEServers: conn.ICEServers,
		})
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		resolve.Invoke(peerconn)
		return
	})
}

func dial(_ js.Value, args []js.Value) interface{} {
	pass := make([]byte, args[0].Length())
	js.CopyBytesToGo(pass, args[0])
	return promise(func(resolve, reject js.Value) {
		err := conn.Dial(string(pass), peerconn)
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		resolve.Invoke()
		return
	})
}

func encode(_ js.Value, args []js.Value) interface{} {
	pass := make([]byte, args[0].Length())
	js.CopyBytesToGo(pass, args[0])
	return strings.Join(wordlist.Encode(pass), "-")
}

func decode(_ js.Value, args []js.Value) interface{} {
	pass := args[0].String()
	buf, _ := wordlist.Decode(strings.Split(pass, "-"))
	log.Println(buf)
	if buf == nil {
		return nil
	}
	dst := js.Global().Get("Uint8Array").New(len(buf))
	js.CopyBytesToJS(dst, buf)
	return dst
}

func main() {
	wormhole.Verbose = true
	js.Global().Set("webwormhole", map[string]interface{}{
		"qrencode": js.FuncOf(qrencode),
		"new":      js.FuncOf(newwormhole),
		"join":     js.FuncOf(joinwormhole),
		"dial":     js.FuncOf(dial),
		"encode":   js.FuncOf(encode),
		"decode":   js.FuncOf(decode),
		//	"match":    js.FuncOf(match),
	})

	// Go wasm executables must remain running. Block indefinitely.
	select {}
}
