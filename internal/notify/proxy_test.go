package notify

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const unreachableTelegramAPI = "http://api.telegram.test"

// directProxySender lets the proxy sit on loopback, where httptest listens and
// the production guard refuses to dial.
func directProxySender() *Sender {
	sender := NewSender(SenderOptions{TelegramAPI: unreachableTelegramAPI})
	sender.proxyTransport = func() *http.Transport { return &http.Transport{} }
	return sender
}

func telegramDestinationVia(proxy string) Destination {
	return Destination{Kind: KindTelegram, Target: "1", Secret: testBotToken, Proxy: proxy}
}

func TestTelegramSendGoesThroughAnHTTPProxyWithCredentials(t *testing.T) {
	var forwardedTo, authorization atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedTo.Store(r.URL.Host + r.URL.Path)
		authorization.Store(r.Header.Get("Proxy-Authorization"))
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(proxy.Close)

	proxyURL := strings.Replace(proxy.URL, "http://", "http://bot:s3cret@", 1)
	err := directProxySender().Send(t.Context(), telegramDestinationVia(proxyURL), sampleNotification, 1)

	if err != nil {
		t.Fatalf("Send = %v", err)
	}
	if want := "api.telegram.test/bot" + testBotToken + "/sendMessage"; forwardedTo.Load() != want {
		t.Fatalf("proxy forwarded to %v, want %s", forwardedTo.Load(), want)
	}
	if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bot:s3cret")); authorization.Load() != want {
		t.Fatalf("Proxy-Authorization = %v, want %s", authorization.Load(), want)
	}
}

func TestTelegramSendGoesThroughASOCKS5ProxyWithCredentials(t *testing.T) {
	telegram, captured := recordingServer(t, http.StatusOK, `{"ok":true}`, nil)
	socks, requested := startSOCKS5(t, "bot", "s3cret", telegram.Listener.Addr().String())
	sender := directProxySender()

	err := sender.Send(t.Context(), telegramDestinationVia("socks5://bot:s3cret@"+socks), sampleNotification, 1)

	if err != nil {
		t.Fatalf("Send = %v", err)
	}
	if captured.path != "/bot"+testBotToken+"/sendMessage" {
		t.Fatalf("telegram received path %q", captured.path)
	}
	if requested.Load() != "api.telegram.test:80" {
		t.Fatalf("SOCKS5 CONNECT asked for %v, want the unresolved api.telegram.test:80", requested.Load())
	}

	err = sender.Send(t.Context(), telegramDestinationVia("socks5://bot:wrong@"+socks), sampleNotification, 1)

	if failure, ok := errors.AsType[*SendError](err); !ok || !strings.HasSuffix(failure.Message, "username/password authentication failed") {
		t.Fatalf("Send with a wrong password = %#v, want the proxy's authentication failure", err)
	}
}

func TestTelegramProxyOnLoopbackIsRefusedBeforeConnecting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var accepted atomic.Bool
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted.Store(true)
			conn.Close()
		}
	}()

	sender := NewSender(SenderOptions{TelegramAPI: unreachableTelegramAPI, AllowPrivateNetworks: true})
	err = sender.Send(t.Context(), telegramDestinationVia("socks5://"+listener.Addr().String()), sampleNotification, 1)

	failure, ok := errors.AsType[*SendError](err)
	if !ok || !failure.Permanent || failure.Message != "telegram: destination address is not allowed: 127.0.0.1" {
		t.Fatalf("Send = %#v, want a permanent refusal of the proxy address", err)
	}
	if accepted.Load() {
		t.Fatal("the refused proxy still received a connection")
	}
}

// startSOCKS5 serves the SOCKS5 CONNECT handshake with username/password
// authentication (RFC 1928, RFC 1929) and pipes every accepted connection to
// upstream, recording the address the client asked for.
func startSOCKS5(t *testing.T, username, password, upstream string) (string, *atomic.Value) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	target := &atomic.Value{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSOCKS5(conn, username, password, upstream, target)
		}
	}()
	return listener.Addr().String(), target
}

func serveSOCKS5(conn net.Conn, username, password, upstream string, target *atomic.Value) {
	defer conn.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, header[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}
	gotUser, gotPassword, ok := readSOCKS5Credentials(conn)
	if !ok || gotUser != username || gotPassword != password {
		_, _ = conn.Write([]byte{1, 1})
		return
	}
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return
	}
	requested, ok := readSOCKS5Target(conn)
	if !ok {
		return
	}
	target.Store(requested)
	upstreamConn, err := net.Dial("tcp", upstream)
	if err != nil {
		return
	}
	defer upstreamConn.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(upstreamConn, conn) }()
	_, _ = io.Copy(conn, upstreamConn)
}

func readSOCKS5Credentials(conn net.Conn) (string, string, bool) {
	fields := make([]string, 0, 2)
	if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
		return "", "", false
	}
	for range 2 {
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", "", false
		}
		value := make([]byte, length[0])
		if _, err := io.ReadFull(conn, value); err != nil {
			return "", "", false
		}
		fields = append(fields, string(value))
	}
	return fields[0], fields[1], true
}

func readSOCKS5Target(conn net.Conn) (string, bool) {
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil {
		return "", false
	}
	var host string
	switch request[3] {
	case 1, 4:
		address := make([]byte, map[byte]int{1: net.IPv4len, 4: net.IPv6len}[request[3]])
		if _, err := io.ReadFull(conn, address); err != nil {
			return "", false
		}
		host = net.IP(address).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return "", false
		}
		name := make([]byte, length[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", false
		}
		host = string(name)
	default:
		return "", false
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), true
}
