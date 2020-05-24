package wormhole

import (
	"io"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v2"
)

// DataChannel wraps webrtc.DataChannel with a blocking Write.
type DataChannel struct {
	rwc io.ReadWriteCloser
	dc  *webrtc.DataChannel
	pc  *webrtc.PeerConnection

	// flushc is a condition variable to coordinate flushed state of the
	// underlying channel.
	flushc *sync.Cond
}

// Read writes a message to the default DataChannel.
func (c *DataChannel) Write(p []byte) (n int, err error) {
	// The webrtc package's channel does not have a blocking Write, so
	// we can't just use io.Copy until the issue is fixed upsteam.
	// Work around this by blocking here and waiting for flushes.
	// https://github.com/pion/sctp/issues/77
	c.flushc.L.Lock()
	for c.dc.BufferedAmount() > c.dc.BufferedAmountLowThreshold() {
		c.flushc.Wait()
	}
	c.flushc.L.Unlock()
	return c.rwc.Write(p)
}

// Read read a message from the default DataChannel.
func (c *DataChannel) Read(p []byte) (n int, err error) {
	return c.rwc.Read(p)
}

// TODO benchmark this buffer madness.
func (c *DataChannel) flushed() {
	c.flushc.L.Lock()
	c.flushc.Signal()
	c.flushc.L.Unlock()
}

// Close attempts to flush the DataChannel buffers then close it
// and its PeerConnection.
func (c *DataChannel) Close() (err error) {
	if Verbose {
		log.Printf("closing")
	}
	for c.dc.BufferedAmount() != 0 {
		// SetBufferedAmountLowThreshold does not seem to take effect
		// when after the last Write().
		time.Sleep(time.Second) // eww.
	}
	tryclose := func(c io.Closer) {
		e := c.Close()
		if e != nil {
			err = e
		}
	}
	defer tryclose(c.pc)
	defer tryclose(c.dc)
	defer tryclose(c.rwc)
	return nil
}
