// +build !js

package wormhole

import "github.com/pion/webrtc/v2"

func (c *Wormhole) defaultPeerConnection() error {
	s := webrtc.SettingEngine{}
	s.DetachDataChannels()
	s.SetTrickle(true)
	rtcapi := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	var err error
	c.pc, err = rtcapi.NewPeerConnection(webrtc.Configuration{
	//	ICEServers: c.ICEServers,
	})
	return err
}

func (c *Wormhole) isRelay() bool {
	stats := c.pc.GetStats()
	for _, s := range stats {
		pairstats, ok := s.(webrtc.ICECandidatePairStats)
		if !ok {
			continue
		}
		if !pairstats.Nominated {
			continue
		}
		local, ok := stats[pairstats.LocalCandidateID].(webrtc.ICECandidateStats)
		if !ok {
			continue
		}
		remote, ok := stats[pairstats.RemoteCandidateID].(webrtc.ICECandidateStats)
		if !ok {
			continue
		}
		if remote.CandidateType == webrtc.ICECandidateTypeRelay ||
			local.CandidateType == webrtc.ICECandidateTypeRelay {
			return true
		}
	}
	return false
}
