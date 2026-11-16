package uplink

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/wxalerts/pws-gateway/internal/contract"
)

// selfSigned returns a server certificate valid for 127.0.0.1 and a pool that
// trusts it.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test broker"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// newWSBroker starts an MQTT broker whose only listener is a WebSocket one
// (TLS when tlsCfg is set), that accepts one username and password. It is how
// the production broker looks to a gateway: wss on 443 at /mqtt.
func newWSBroker(t *testing.T, tlsCfg *tls.Config, user, pass string) (*testBroker, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	srv := mqtt.New(&mqtt.Options{InlineClient: true, Logger: quietLogger()})
	ledger := &auth.Ledger{Auth: auth.AuthRules{{Username: auth.RString(user), Password: auth.RString(pass), Allow: true}}}
	if err := srv.AddHook(new(auth.Hook), &auth.Options{Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	if err := srv.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: addr, TLSConfig: tlsCfg})); err != nil {
		t.Fatal(err)
	}
	b := &testBroker{srv: srv, addr: addr}
	if err := srv.Subscribe("pws/#", 1, func(_ *mqtt.Client, _ packets.Subscription, pk packets.Packet) {
		b.mu.Lock()
		b.got = append(b.got, received{Topic: pk.TopicName, Payload: append([]byte(nil), pk.Payload...), Retain: pk.FixedHeader.Retain, At: time.Now()})
		b.mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	waitFor(t, 5*time.Second, "websocket broker listening", func() bool {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	})
	return b, addr
}

func TestConnectsOverWebSocketTLSWithCredentials(t *testing.T) {
	cert, pool := selfSigned(t)
	b, addr := newWSBroker(t, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, testDevice, "ws-secret-pw")
	r := newRig(t, t0)
	u := r.uplink(func(o *Options) {
		o.BrokerURL = "wss://" + addr + "/mqtt"
		o.Credentials = Credentials{Username: testDevice, Password: "ws-secret-pw"}
		o.TLS = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	})
	r.run(u)

	statusTopic := contract.Topic(testDevice, contract.KindStatus)
	waitFor(t, 5*time.Second, "retained online over wss", func() bool {
		m := b.retained(statusTopic)
		return len(m) == 1 && string(m[0].Payload) == `"online"`
	})
	if !u.Status().Connected {
		t.Fatal("uplink reports offline")
	}
}

func TestConnectsOverPlainWebSocket(t *testing.T) {
	b, addr := newWSBroker(t, nil, testDevice, "ws-secret-pw")
	r := newRig(t, t0)
	u := r.uplink(func(o *Options) {
		o.BrokerURL = "ws://" + addr + "/mqtt"
		o.Credentials = Credentials{Username: testDevice, Password: "ws-secret-pw"}
	})
	r.run(u)
	waitFor(t, 5*time.Second, "retained online over ws", func() bool {
		return len(b.retained(contract.Topic(testDevice, contract.KindStatus))) == 1
	})
}

func TestWebSocketBrokerRejectsAWrongPassword(t *testing.T) {
	b, addr := newWSBroker(t, nil, testDevice, "right-password")
	r := newRig(t, t0)
	u := r.uplink(func(o *Options) {
		o.BrokerURL = "ws://" + addr + "/mqtt"
		o.Credentials = Credentials{Username: testDevice, Password: "wrong-password"}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = u.Run(ctx) }()
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done
	if u.Status().Connected || len(b.retained(contract.Topic(testDevice, contract.KindStatus))) != 0 {
		t.Fatal("a wrong password must not connect")
	}
}
