// Package wormhole implements a signalling protocol to establish password protected
// WebRTC connections between peers.
//
// WebRTC uses DTLS-SRTP (https://tools.ietf.org/html/rfc5764) to secure its
// data. The mechanism it uses to exchange keys relies on exchanging metadata
// that includes both endpoints' certificate fingerprints via some trusted channel,
// typically a signalling server over https and websockets. More in RFC5763
// (https://tools.ietf.org/html/rfc5763).
//
// This package removes the signalling server from the trust model by using a
// PAKE to estabish the authenticity of the WebRTC metadata. In other words,
// it's a clone of Magic Wormhole made to use WebRTC as the transport.
//
// The protocol requires a signalling server that facilitates exchanging
// arbitrary messages via a slot system. The server subcommand of the
// ww tool is an implementation of this over WebSockets.
//
// Rough sketch of the handshake:
//
//	Peer               Signalling Server                Peer
//	----open------------------> |
//	<---new_slot,TURN_ticket--- |
//	                            | <------------------open----
//	                            | ------------TURN_ticket--->
//	<---------------------------|--------------pake_msg_a----
//	----pake_msg_b--------------|--------------------------->
//	----sbox(offer)-------------|--------------------------->
//	<---------------------------|------------sbox(answer)----
//	----sbox(candidates...)-----|--------------------------->
//	<---------------------------|-----sbox(candidates...)----
package wormhole

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"filippo.io/cpace"
	webrtc "github.com/pion/webrtc/v2"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
	"nhooyr.io/websocket"
)

// Protocol is an identifier for the current signalling scheme. It's
// intended to help clients print a friendlier message urging them to
// upgrade if the signalling server has a different version.
const Protocol = "4"

const (
	// CloseNoSuchSlot is the WebSocket status returned if the slot is not valid.
	CloseNoSuchSlot = 4000 + iota

	// CloseSlotTimedOut is the WebSocket status returned when the slot times out.
	CloseSlotTimedOut

	// CloseNoMoreSlots is the WebSocket status returned when the signalling server
	// cannot allocate any new slots at the time.
	CloseNoMoreSlots

	// CloseWrongProto is the WebSocket status returned when the signalling server
	// runs a different version of the signalling protocol.
	CloseWrongProto

	// ClosePeerHungUp is the WebSocket status returned when the peer has closed
	// its connection.
	ClosePeerHungUp

	// CloseBadKey is the WebSocket status returned when the peer has closed its
	// connection because the key it derived is bad.
	CloseBadKey

	// TODO move these out of this package.
)

var (
	// ErrBadVersion is returned when the signalling server runs an incompatible
	// version of the signalling protocol.
	ErrBadVersion = errors.New("bad version")

	// ErrBadVersion is returned when the the peer on the same slot uses a different
	// password.
	ErrBadKey = errors.New("bad key")

	// ErrNoSuchSlot indicates no one is on the slot requested.
	ErrNoSuchSlot = errors.New("no such slot")

	// ErrNoSuchSlot indicates signalling has timed out.
	ErrTimedOut = errors.New("timed out")
)

// Verbose logging.
var Verbose = false

// A Wormhole is a WebRTC connection established via the WebWormhole signalling
// protocol. It is wraps webrtc.PeerConnection and webrtc.DataChannel.
type Wormhole struct {
	pc   *webrtc.PeerConnection
	ws   *websocket.Conn
	side int
	key  [32]byte

	Slot       string
	ICEServers []webrtc.ICEServer

	localCandidate  chan struct{}
	remoteCandidate chan struct{}
}

func readEncJSON(ws *websocket.Conn, key *[32]byte, v interface{}) error {
	_, buf, err := ws.Read(context.TODO())
	if err != nil {
		return err
	}
	encrypted, err := base64.URLEncoding.DecodeString(string(buf))
	if err != nil {
		return err
	}
	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	jsonmsg, ok := secretbox.Open(nil, encrypted[24:], &nonce, key)
	if !ok {
		return ErrBadKey
	}
	return json.Unmarshal(jsonmsg, v)
}

func writeEncJSON(ws *websocket.Conn, key *[32]byte, v interface{}) error {
	jsonmsg, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var nonce [24]byte
	if _, err := io.ReadFull(crand.Reader, nonce[:]); err != nil {
		return err
	}
	return ws.Write(
		context.TODO(),
		websocket.MessageText,
		[]byte(base64.URLEncoding.EncodeToString(
			secretbox.Seal(nonce[:], jsonmsg, &nonce, key),
		)),
	)
}

func readBase64(ws *websocket.Conn) ([]byte, error) {
	_, buf, err := ws.Read(context.TODO())
	if err != nil {
		return nil, err
	}
	return base64.URLEncoding.DecodeString(string(buf))
}

func writeBase64(ws *websocket.Conn, p []byte) error {
	return ws.Write(
		context.TODO(),
		websocket.MessageText,
		[]byte(base64.URLEncoding.EncodeToString(p)),
	)
}

// readInitMsg reads the first message the signalling server sends over
// the WebSocket connection, which has metadata includign assigned slot
// and ICE servers to use.
func readInitMsg(ws *websocket.Conn) (slot string, iceServers []webrtc.ICEServer, err error) {
	msg := struct {
		Slot       string             `json:"slot",omitempty`
		ICEServers []webrtc.ICEServer `json:"iceServers",omitempty`
	}{}
	_, buf, err := ws.Read(context.TODO())
	if err != nil {
		return
	}
	err = json.Unmarshal(buf, &msg)
	return msg.Slot, msg.ICEServers, err
}

// handleRemoteCandidates waits for remote candidate to trickle in. We close
// the websocket when we get a successful connection so this should fail and
// exit at some point.
func (c *Wormhole) handleRemoteCandidates() {
	defer close(c.remoteCandidate)
	for {
		var candidate webrtc.ICECandidateInit
		err := readEncJSON(c.ws, &c.key, &candidate)
		if err != nil {
			if Verbose {
				log.Printf("cannot read remote candidate: %v", err)
			}
			return
		}
		if candidate.Candidate == "" {
			if Verbose {
				log.Printf("no more remote candidates")
			}
			return
		}
		if Verbose {
			log.Printf("received new remote candidate")
		}
		err = c.pc.AddICECandidate(candidate)
		if err != nil {
			if Verbose {
				log.Printf("cannot add candidate: %v", err)
			}
		}
	}
}

// handleLocalCandidates is the callback for whenever a new local candidate
// is discovered.
func (c *Wormhole) handleLocalCandidates(candidate *webrtc.ICECandidate) {
	log.Printf("debug: got new local candidate %v", candidate)
	if candidate == nil {
		// We can't rely on browsers not invoking this after already giving us a
		// nil candidate.
		select {
		case <-c.localCandidate:
			// Already got a nil candidate and closed channel. Do Nothing.
		default:
			if Verbose {
				logNAT(c.pc.LocalDescription().SDP)
			}
			writeEncJSON(c.ws, &c.key, webrtc.ICECandidateInit{})
			close(c.localCandidate)
		}
		return
	}
	err := writeEncJSON(c.ws, &c.key, candidate.ToJSON())
	if Verbose {
		if err != nil {
			log.Printf("cannot send local candidate: %v", err)
		} else {
			log.Printf("sent new local candidate")
		}
	}
}

// IsRelay returns whether this connection is over a TURN relay or not.
//
// On JS it currently panics.
func (c *Wormhole) IsRelay() bool {
	return c.isRelay()
}

// DialDataChannel finishes the signalling handshake with default configuration
// for the PeerConnection: a single prenegotiated datachannel "data" with id 0.
//
// Calling DialDataChannel on a Wormhole object that is already established
// panics.
func (c *Wormhole) DialDataChannel(pass string) (*DataChannel, error) {
	if c.side == sideNone {
		panic("called dial twice on wormhole")
	}

	err := c.defaultPeerConnection()
	if err != nil {
		return nil, err
	}

	d := &DataChannel{
		pc:     c.pc,
		flushc: sync.NewCond(&sync.Mutex{}),
	}
	sigh := true
	d.dc, err = c.pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Negotiated: &sigh,
		ID:         new(uint16),
	})
	if err != nil {
		return nil, err
	}

	opened := make(chan error)
	d.dc.OnOpen(func() {
		var err error
		d.rwc, err = d.dc.Detach()
		opened <- err
	})
	d.dc.OnBufferedAmountLow(d.flushed)
	// Any threshold amount >= 1MiB seems to occasionally lock up pion.
	// Choose 512 KiB as a safe default.
	d.dc.SetBufferedAmountLowThreshold(512 << 10)

	switch c.side {
	case sideNew:
		err = c.finishNew(pass)
	case sideJoin:
		err = c.finishJoin(pass)
	}
	if err != nil {
		return nil, err
	}

	select {
	case err = <-opened:
		if err != nil {
			return nil, err
		}
		if Verbose {
			log.Printf("datachannel opened, closing signalling channel")
		}
		c.ws.Close(websocket.StatusNormalClosure, "done")
		return d, nil
	case <-time.After(30 * time.Second):
		c.ws.Close(websocket.StatusNormalClosure, "timed out")
		return nil, ErrTimedOut
	}
}

// Dial finishes the signalling handshake using the given PeerConnection object,
//
// Calling Dial on a Wormhole object that is already established panics.
func (c *Wormhole) Dial(pass string, pc *webrtc.PeerConnection) error {
	c.pc = pc
	if c.side == sideNone {
		panic("called dial twice on wormhole")
	}

	var err error
	switch c.side {
	case sideNew:
		err = c.finishNew(pass)
	case sideJoin:
		err = c.finishJoin(pass)
	}
	if err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		<-c.remoteCandidate
		<-c.localCandidate
		close(done)
	}()

	select {
	case <-done:
		if Verbose {
			log.Printf("signalling finished, closing signalling channel")
		}
		c.ws.Close(websocket.StatusNormalClosure, "done")
		return nil
	case <-time.After(30 * time.Second):
		c.ws.Close(websocket.StatusNormalClosure, "timed out")
		return ErrTimedOut
	}
}

// Which side of the handshake, in order for Dial and DialDataChannel pickup where
// New or Join have left off.
const (
	sideNone = iota
	sideNew
	sideJoin
)

// New starts a new signalling handshake after asking the server to allocate
// a new slot.
//
// The slot is used to synchronise with the remote peer on signalling server
// sigserv, and pass is used as the PAKE password authenticate the WebRTC
// offer and answer.
//
// The server generated slot identifier is written on slotc.
//
// If pc is nil it initialises ones using the default STUN server.
func New(sigserv string) (*Wormhole, error) {
	return newWormhole(sigserv, "", sideNew)
}

// Join performs the signalling handshake to join an existing slot.
//
// slot is used to synchronise with the remote peer on signalling server
// sigserv, and pass is used as the PAKE password authenticate the WebRTC
// offer and answer.
//
// If pc is nil it initialises ones using the default STUN server.
func Join(sigserv, slot string) (*Wormhole, error) {
	return newWormhole(sigserv, slot, sideJoin)
}

func unwrapWebsocketErr(err error) error {
	switch websocket.CloseStatus(err) {
	case CloseWrongProto:
		return ErrBadVersion
	case CloseNoSuchSlot:
		return ErrNoSuchSlot
	case CloseSlotTimedOut:
		return ErrTimedOut
	default:
		return err
	}
}

func newWormhole(sigserv, slot string, side int) (w *Wormhole, err error) {
	defer func() { err = unwrapWebsocketErr(err) }()

	u, err := url.Parse(sigserv)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" || u.Scheme == "ws" {
		u.Scheme = "ws"
	} else {
		u.Scheme = "wss"
	}
	u.Path += slot
	u.Fragment = ""
	wsaddr := u.String()

	// Start the handshake.
	ws, _, err := websocket.Dial(context.TODO(), wsaddr, &websocket.DialOptions{
		Subprotocols: []string{Protocol},
	})
	if err != nil {
		return nil, err
	}

	assignedSlot, iceServers, err := readInitMsg(ws)
	if err != nil {
		return nil, err
	}
	if Verbose {
		log.Printf("connected to signalling server on slot: %v", assignedSlot)
	}

	return &Wormhole{
		Slot:            assignedSlot,
		ICEServers:      iceServers,
		side:            side,
		ws:              ws,
		localCandidate:  make(chan struct{}),
		remoteCandidate: make(chan struct{}),
	}, nil
}

func (c *Wormhole) finishNew(pass string) error {
	c.side = sideNone
	msgA, err := readBase64(c.ws)
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("got A pake msg (%v bytes)", len(msgA))
	}
	msgB, mk, err := cpace.Exchange(pass, cpace.NewContextInfo("", "", nil), msgA)
	if err != nil {
		return err
	}
	_, err = io.ReadFull(hkdf.New(sha256.New, mk, nil, nil), c.key[:])
	if err != nil {
		return err
	}
	err = writeBase64(c.ws, msgB)
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("have key, sent B pake msg (%v bytes)", len(msgB))
	}
	c.pc.OnICECandidate(c.handleLocalCandidates)
	offer, err := c.pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	err = writeEncJSON(c.ws, &c.key, offer)
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("sent offer")
	}
	var answer webrtc.SessionDescription
	err = readEncJSON(c.ws, &c.key, &answer)
	if websocket.CloseStatus(err) == CloseBadKey {
		return ErrBadKey
	}
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("got answer")
	}
	err = c.pc.SetLocalDescription(offer)
	if err != nil {
		return err
	}
	err = c.pc.SetRemoteDescription(answer)
	if err != nil {
		return err
	}
	go c.handleRemoteCandidates()
	return nil
}

func (c *Wormhole) finishJoin(pass string) error {
	// The identity arguments are to bind endpoint identities in PAKE. Cf. Unknown
	// Key-Share Attack. https://tools.ietf.org/html/draft-ietf-mmusic-sdp-uks-03
	//
	// In the context of a program like magic-wormhole we do not have ahead of time
	// information on the identity of the remote party. We only have the slot name,
	// and sometimes even that at this stage. But that's okay, since:
	//   a) The password is randomly generated and ephemeral.
	//   b) A peer only gets one guess.
	// An unintended destination is likely going to fail PAKE.
	msgA, pake, err := cpace.Start(pass, cpace.NewContextInfo("", "", nil))
	if err != nil {
		return err
	}
	err = writeBase64(c.ws, msgA)
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("sent A pake msg (%v bytes)", len(msgA))
	}
	msgB, err := readBase64(c.ws)
	if err != nil {
		return err
	}
	mk, err := pake.Finish(msgB)
	if err != nil {
		return err
	}
	_, err = io.ReadFull(hkdf.New(sha256.New, mk, nil, nil), c.key[:])
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("have key, got B msg (%v bytes)", len(msgB))
	}
	c.pc.OnICECandidate(c.handleLocalCandidates)
	var offer webrtc.SessionDescription
	err = readEncJSON(c.ws, &c.key, &offer)
	if err == ErrBadKey {
		// Close with the right status so the other side knows to quit immediately.
		c.ws.Close(CloseBadKey, "bad key")
		return err
	}
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("got offer")
	}
	err = c.pc.SetRemoteDescription(offer)
	if err != nil {
		return err
	}
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	err = writeEncJSON(c.ws, &c.key, answer)
	if err != nil {
		return err
	}
	if Verbose {
		log.Printf("sent answer")
	}
	err = c.pc.SetLocalDescription(answer)
	if err != nil {
		return err
	}
	go c.handleRemoteCandidates()
	return nil
}

// logNAT tries to guess the type of NAT based on candidates and log it.
func logNAT(sdp string) {
	count, host, srflx := 0, 0, 0
	portmap := map[string]map[string]bool{}
	lines := strings.Split(strings.ReplaceAll(sdp, "\r", ""), "\n")
	for _, l := range lines {
		if !strings.HasPrefix(l, "a=candidate:") {
			continue
		}
		parts := strings.Split(l[len("a=candidate:"):], " ")
		proto := strings.ToLower(parts[2])
		port := parts[5]
		typ := parts[7]
		if proto != "udp" {
			continue
		}
		count++
		if typ == "host" {
			host++
		} else if typ == "srflx" {
			srflx++
			var rport string
			for i := 8; i < len(parts); i += 2 {
				if parts[i] == "rport" {
					rport = parts[i+1]
					break
				}
			}
			if portmap[rport] == nil {
				portmap[rport] = map[string]bool{}
			}
			portmap[rport][port] = true
		}
	}
	log.Printf("local udp candidates: %d (host: %d stun: %d)", count, host, srflx)
	maxmapping := 0
	for _, v := range portmap {
		if len(v) > maxmapping {
			maxmapping = len(v)
		}
	}
	switch maxmapping {
	case 0:
		log.Printf("nat: unknown: ice disabled or stun blocked")
	case 1:
		if srflx == 1 {
			log.Printf("nat: not enough stun servers to tell")
		} else {
			log.Printf("nat: 1:1 port mapping")
		}
	default:
		log.Printf("nat: symmetric: 1:n port mapping (bad news)")
	}
}
