package wormhole

import "github.com/pion/webrtc/v2"

func (c *Wormhole) defaultPeerConnection() error {
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	rtcapi := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	var err error
	c.pc, err = rtcapi.NewPeerConnection(webrtc.Configuration{
		ICEServers: c.ICEServers,
	})
	return err
}

// As of today, GetStats() is not implemented in Pion's WebAssembly target.
func (c *Wormhole) isRelay() bool { panic("not implemented") }
