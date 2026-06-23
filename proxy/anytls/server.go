package anytls

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/nadoo/glider/pkg/log"
	"github.com/nadoo/glider/pkg/socks"
	"github.com/nadoo/glider/proxy"
)

func init() {
	proxy.RegisterServer("anytls", NewAnyTLSServer)
}

func NewAnyTLSServer(s string, p proxy.Proxy) (proxy.Server, error) {
	a, err := NewAnyTLS(s, nil, p)
	if err != nil {
		return nil, fmt.Errorf("[anytls] create instance error: %s", err)
	}
	if a.certFile == "" || a.keyFile == "" {
		return nil, errors.New("[anytls] cert and key file path must be specified")
	}
	cert, err := tls.LoadX509KeyPair(a.certFile, a.keyFile)
	if err != nil {
		return nil, fmt.Errorf("[anytls] unable to load cert: %s, key %s, error: %s", a.certFile, a.keyFile, err)
	}
	a.tlsConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	return a, nil
}

func (s *AnyTLS) ListenAndServe() {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		log.Fatalf("[anytls] failed to listen on %s: %v", s.addr, err)
		return
	}
	defer l.Close()

	log.F("[anytls] listening TCP on %s", s.addr)
	for {
		c, err := l.Accept()
		if err != nil {
			log.F("[anytls] failed to accept: %v", err)
			continue
		}
		go s.Serve(c)
	}
}

func (s *AnyTLS) Serve(c net.Conn) {
	tlsConn := tls.Server(c, s.tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		_ = tlsConn.Close()
		log.F("[anytls] error in tls handshake: %s", err)
		return
	}
	if err := readAuth(tlsConn, s.password); err != nil {
		_ = tlsConn.Close()
		log.F("[anytls] auth error from %s: %s", c.RemoteAddr(), err)
		return
	}

	ss := newSession(tlsConn)
	ss.start()
	for {
		st, err := ss.acceptStream()
		if err != nil {
			_ = ss.Close()
			return
		}
		go s.serveStream(ss, st)
	}
}

func (s *AnyTLS) serveStream(ss *session, st *stream) {
	defer st.Close()

	target, err := socks.ReadAddr(st)
	if err != nil {
		_ = ss.writeFrame(frame{command: cmdSYNACK, streamID: st.id, data: []byte(err.Error())})
		log.F("[anytls] read target error: %v", err)
		return
	}

	rc, dialer, err := s.proxy.Dial("tcp", target.String())
	if err != nil {
		_ = ss.writeFrame(frame{command: cmdSYNACK, streamID: st.id, data: []byte(err.Error())})
		log.F("[anytls] %s <-> %s via %s, error in dial: %v", st.RemoteAddr(), target, dialer.Addr(), err)
		return
	}
	defer rc.Close()

	_ = ss.writeFrame(frame{command: cmdSYNACK, streamID: st.id})
	log.F("[anytls] %s <-> %s via %s", st.RemoteAddr(), target, dialer.Addr())
	if err = proxy.Relay(st, rc); err != nil {
		log.F("[anytls] %s <-> %s via %s, relay error: %v", st.RemoteAddr(), target, dialer.Addr(), err)
		if !strings.Contains(err.Error(), s.addr) {
			s.proxy.Record(dialer, false)
		}
	}
}
