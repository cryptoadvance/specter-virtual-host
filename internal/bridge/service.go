package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cryptoadvance/specter-virtual-host/internal/model"
	"github.com/cryptoadvance/specter-virtual-host/internal/policy"
)

type SettingsProvider func() model.Settings
type ActivityWriter func(kind, origin, message, result string)

const (
	requestTimeout           = 30 * time.Second
	maxPendingRequests       = 32
	maxHostConnections       = 64
	maxBrowserConnections    = 4
	hostFirstRequestTimeout  = 5 * time.Second
	hostResponseWriteTimeout = 15 * time.Second
	browserPingInterval      = 20 * time.Second
	browserReadTimeout       = 60 * time.Second
)

type pendingRequest struct {
	request model.PendingRequest
	done    chan struct{}
	allowed bool
}

type browserSession struct {
	ws       *wsConn
	clientID string
	origin   string
}

type Service struct {
	mu                        sync.Mutex
	hostSessionMu             sync.Mutex
	generation                uint64
	version                   string
	webAddress                string
	hwiAddress                string
	settings                  SettingsProvider
	activity                  ActivityWriter
	running                   bool
	walletAllowed             bool
	browser                   *wsConn
	browserSessions           []*browserSession
	browserReservations       int
	browserClientID           string
	browserOrigin             string
	host                      net.Conn
	hostBrowser               *wsConn
	hostBrowserClientID       string
	hostRequestID             string
	hostCommand               string
	hostRequestStarted        bool
	hostResponseLines         int
	hostResponseBytes         int
	hostSequence              uint64
	firstHostRequestTimeout   time.Duration
	hostConnections           map[net.Conn]struct{}
	quarantinedBrowserClients map[string]struct{}
	legacyBrowserQuarantined  bool
	webServer                 *http.Server
	webListener               net.Listener
	hwiListener               net.Listener
	pending                   map[string]*pendingRequest
	pendingByOrigin           map[string]*pendingRequest
}

func New(version string, settings SettingsProvider, activity ActivityWriter) *Service {
	return NewWithAddresses(version, model.WebAddress, model.HWIAddress, settings, activity)

}

func NewWithAddresses(version, webAddress, hwiAddress string, settings SettingsProvider, activity ActivityWriter) *Service {
	return &Service{
		version: version, webAddress: webAddress, hwiAddress: hwiAddress,
		settings: settings, activity: activity, walletAllowed: true,
		pending: make(map[string]*pendingRequest), pendingByOrigin: make(map[string]*pendingRequest),
		hostConnections:           make(map[net.Conn]struct{}),
		quarantinedBrowserClients: make(map[string]struct{}),
		firstHostRequestTimeout:   hostFirstRequestTimeout,
	}
}

func (s *Service) Status() model.BridgeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return model.BridgeStatus{
		Running: s.running, BrowserConnected: s.browser != nil,
		BrowserRecoveryRequired: s.browser != nil && s.browserQuarantinedLocked(s.browser, s.browserClientID),
		BrowserOrigin:           s.browserOrigin,
		WalletConnected:         s.host != nil, WalletAllowed: s.walletAllowed,
		WebAddress: s.webAddress, HWIAddress: s.hwiAddress,
	}
}

func (s *Service) PendingRequests() []model.PendingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	requests := make([]model.PendingRequest, 0, len(s.pending))
	for _, request := range s.pending {
		requests = append(requests, request.request)
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].RequestedAt.Before(requests[j].RequestedAt) })
	return requests
}

// ResolvePending releases outstanding browser handshakes when a policy change
// makes their approval prompts obsolete.
func (s *Service) ResolvePending(allowed bool) {
	for _, request := range s.PendingRequests() {
		_, _, _ = s.ResolveRequest(request.ID, allowed, nil)
	}
}

// ResolveRequest runs beforeAllow while the request is still pending. It lets
// permanent approval save the origin before any waiting bridge handshake is
// released, without racing the request timeout.
func (s *Service) ResolveRequest(id string, allowed bool, beforeAllow func(string) error) (model.PendingRequest, bool, error) {
	s.mu.Lock()
	request, exists := s.pending[id]
	if !exists {
		s.mu.Unlock()
		return model.PendingRequest{}, false, errors.New("connection request expired or was already handled")
	}
	if allowed && beforeAllow != nil {
		if err := beforeAllow(request.request.Origin); err != nil {
			s.mu.Unlock()
			return model.PendingRequest{}, false, err
		}
	}
	delete(s.pending, id)
	delete(s.pendingByOrigin, request.request.Origin)
	request.allowed = allowed
	close(request.done)
	resolved := request.request
	s.mu.Unlock()
	result := "denied"
	message := "Website connection denied"
	if allowed {
		result = "allowed-once"
		message = "Website connection allowed once"
		if beforeAllow != nil {
			result = "trusted"
			message = "Website added to trusted sites"
		}
	}
	s.addActivity("website", resolved.Origin, message, result)
	return resolved, true, nil
}

func (s *Service) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	hwiListener, err := net.Listen("tcp", s.hwiAddress)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("cannot open HWI endpoint %s: %w", s.hwiAddress, err)
	}
	webListener, err := net.Listen("tcp", s.webAddress)
	if err != nil {
		_ = hwiListener.Close()
		s.mu.Unlock()
		return fmt.Errorf("cannot open browser bridge %s: %w", s.webAddress, err)
	}
	mux := s.routes()
	server := &http.Server{Addr: s.webAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	s.hwiListener = hwiListener
	s.webListener = webListener
	s.webServer = server
	s.running = true
	s.generation++
	generation := s.generation
	s.walletAllowed = true
	s.mu.Unlock()

	go s.acceptHosts(hwiListener, generation)
	go func() {
		if err := server.Serve(webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Browser bridge stopped unexpectedly: %v", err)
		}
	}()
	s.addActivity("bridge", "", "Bridge started", "active")
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	server := s.webServer
	webListener := s.webListener
	hwiListener := s.hwiListener
	quarantined := s.quarantineHostBrowserLocked()
	hosts := make([]net.Conn, 0, len(s.hostConnections))
	for connection := range s.hostConnections {
		hosts = append(hosts, connection)
	}
	if s.host != nil {
		if _, found := s.hostConnections[s.host]; !found {
			hosts = append(hosts, s.host)
		}
	}
	browsers := make([]*wsConn, 0, len(s.browserSessions))
	for _, session := range s.browserSessions {
		browsers = append(browsers, session.ws)
	}
	if len(browsers) == 0 && s.browser != nil {
		browsers = append(browsers, s.browser)
	}
	s.running = false
	s.generation++
	s.webServer = nil
	s.webListener = nil
	s.hwiListener = nil
	s.browser = nil
	s.browserSessions = nil
	s.browserOrigin = ""
	s.host = nil
	s.hostBrowser = nil
	s.hostBrowserClientID = ""
	s.clearHostTransactionLocked()
	for id, request := range s.pending {
		request.allowed = false
		close(request.done)
		delete(s.pending, id)
	}
	s.pendingByOrigin = make(map[string]*pendingRequest)
	s.mu.Unlock()
	if quarantined {
		s.addActivity("request", "", "An interrupted HWI response requires a simulator page reload before retrying", "recovery-required")
	}
	for _, browser := range browsers {
		_ = browser.conn.Close()
	}
	for _, connection := range hosts {
		_ = connection.Close()
	}
	if hwiListener != nil {
		_ = hwiListener.Close()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	if webListener != nil {
		_ = webListener.Close()
	}
	s.addActivity("bridge", "", "Bridge stopped", "stopped")
	return nil
}

func (s *Service) DisconnectWallet() {
	s.mu.Lock()
	s.walletAllowed = false
	quarantined := s.quarantineHostBrowserLocked()
	hosts := make([]net.Conn, 0, len(s.hostConnections))
	for connection := range s.hostConnections {
		hosts = append(hosts, connection)
	}
	if s.host != nil {
		if _, found := s.hostConnections[s.host]; !found {
			hosts = append(hosts, s.host)
		}
	}
	s.host = nil
	s.hostBrowser = nil
	s.hostBrowserClientID = ""
	s.clearHostTransactionLocked()
	s.mu.Unlock()
	for _, connection := range hosts {
		_ = connection.Close()
	}
	s.sendStatus("host", false)
	if quarantined {
		s.addActivity("request", "", "An interrupted HWI response requires a simulator page reload before retrying", "recovery-required")
	}
	s.addActivity("wallet", "", "Wallet connections disabled", "blocked")
}

func (s *Service) AllowWallet() {
	s.mu.Lock()
	s.walletAllowed = true
	s.mu.Unlock()
	s.addActivity("wallet", "", "Wallet connections enabled", "allowed")
}

func (s *Service) clearHostTransactionLocked() {
	s.hostRequestID = ""
	s.hostCommand = ""
	s.hostRequestStarted = false
	s.hostResponseLines = 0
	s.hostResponseBytes = 0
}

func (s *Service) hostRequestTimeout() time.Duration {
	if s.firstHostRequestTimeout > 0 {
		return s.firstHostRequestTimeout
	}
	return hostFirstRequestTimeout
}

func (s *Service) quarantineHostBrowserLocked() bool {
	if !s.hostRequestStarted || s.hostResponseLines >= 2 || s.hostBrowser == nil {
		return false
	}
	s.quarantineBrowserLocked(s.hostBrowser, s.hostBrowserClientID)
	return true
}

func (s *Service) quarantineBrowserLocked(ws *wsConn, clientID string) {
	if ws == nil {
		return
	}
	if clientID == "" {
		s.legacyBrowserQuarantined = true
		return
	}
	if s.quarantinedBrowserClients == nil {
		s.quarantinedBrowserClients = make(map[string]struct{})
	}
	s.quarantinedBrowserClients[clientID] = struct{}{}
}

func (s *Service) browserQuarantinedLocked(ws *wsConn, clientID string) bool {
	if clientID == "" {
		return s.legacyBrowserQuarantined
	}
	_, quarantined := s.quarantinedBrowserClients[clientID]
	return quarantined
}

func (s *Service) ConnectedURL() string {
	return "http://" + s.webAddress + "/connected"
}

func (s *Service) OpenSimulator() error {
	status := s.Status()
	if !status.Running {
		return errors.New("bridge is not running")
	}
	if status.BrowserConnected {
		return errors.New("a simulator is already connected; use its existing browser tab to preserve its session")
	}
	return openBrowser(s.ConnectedURL())
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/bridge", s.serveBridge)
	mux.HandleFunc("/virtual-host-health", s.serveHealth)
	mux.HandleFunc("/connected", s.serveConnected)
	mux.HandleFunc("/", s.serveProxy)
	return mux
}

func (s *Service) serveBridge(w http.ResponseWriter, r *http.Request) {
	origin, allowed := s.requestOrigin(r)
	if !allowed && origin != "" {
		allowed = s.RequestApproval(origin)
	}
	if !allowed {
		s.addActivity("website", origin, "Website requested bridge access", "blocked")
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	clientID := r.URL.Query().Get("client")
	if !s.acceptsBrowserClient(clientID) {
		http.Error(w, "open the newest connected simulator tab", http.StatusConflict)
		return
	}
	if !s.reserveBrowserConnection() {
		s.addActivity("website", origin, "Website connection limit reached", "blocked")
		http.Error(w, "the bridge already has four connected websites", http.StatusTooManyRequests)
		return
	}
	ws, err := upgradeWebSocket(w, r, func(request *http.Request) bool {
		if request.Header.Get("Origin") == "" {
			return true
		}
		_, allowed := s.requestOrigin(request)
		return allowed
	})
	if err != nil {
		s.releaseBrowserReservation()
		log.Printf("Unable to upgrade browser WebSocket: %v", err)
		return
	}
	s.addActivity("website", origin, "Website connected to the bridge", "allowed")
	s.handleBrowserWithReservation(ws, clientID, origin)
}

func (s *Service) reserveBrowserConnection() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.browserSessions)+s.browserReservations >= maxBrowserConnections {
		return false
	}
	s.browserReservations++
	return true
}

func (s *Service) releaseBrowserReservation() {
	s.mu.Lock()
	if s.browserReservations > 0 {
		s.browserReservations--
	}
	s.mu.Unlock()
}

func (s *Service) serveHealth(w http.ResponseWriter, r *http.Request) {
	origin, allowed := s.requestOrigin(r)
	if headerOrigin := r.Header.Get("Origin"); headerOrigin != "" && allowed {
		w.Header().Set("Access-Control-Allow-Origin", headerOrigin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": s.version, "origin": origin})
}

func (s *Service) serveConnected(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query := url.Values{
		"virtual-host": {"1"}, "variant": {"diy"}, "virtual-host-version": {s.version},
	}
	if r.URL.Query().Get("probe") == "usb" {
		query.Set("probe", "usb")
	}
	http.Redirect(w, r, "http://"+s.webAddress+"/?"+query.Encode(), http.StatusFound)
}

func (s *Service) serveProxy(w http.ResponseWriter, r *http.Request) {
	settings := s.settings()
	upstream, err := url.Parse(settings.Site)
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") {
		http.Error(w, "The configured simulator site is invalid", http.StatusBadGateway)
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		query := r.URL.Query()
		if query.Get("virtual-host") == "1" {
			query.Del("manifest")
			query.Del("buildVariant")
			query.Set("variant", "diy")
			r.URL.RawQuery = query.Encode()
		}
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	// NewSingleHostReverseProxy rewrites the request URL but deliberately keeps
	// the incoming Host header. Here the incoming Host is 127.0.0.1:8788; sites
	// behind virtual hosting (including try.clavastack.com) then serve their
	// empty default page instead of the simulator. Forward the configured host
	// so the upstream serves the actual site.
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = upstream.Host
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Set("X-Specter-Virtual-Host", s.version)
		contentType := response.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "json") {
			response.Header.Set("Cache-Control", "no-store")
			response.Header.Del("ETag")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "The simulator site could not be reached: "+err.Error(), http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (s *Service) requestOrigin(r *http.Request) (string, bool) {
	rawOrigin := r.Header.Get("Origin")
	normalized, err := policy.NormalizeOrigin(rawOrigin)
	if err != nil {
		return "", false
	}
	parsed, _ := url.Parse(normalized)
	if s.isBridgeLoopback(parsed) {
		upstream, err := url.Parse(s.settings().Site)
		if err == nil {
			rawOrigin = upstream.Scheme + "://" + upstream.Host
		}
	}
	return policy.Allows(s.settings(), rawOrigin)
}

func (s *Service) RequestApproval(origin string) bool {
	settings := s.settings()
	if _, allowed := policy.Allows(settings, origin); allowed {
		return true
	}
	if settings.OriginPolicy != model.OriginPolicyTrusted || !settings.NotifyNewSite {
		return false
	}
	s.mu.Lock()
	request := s.pendingByOrigin[origin]
	if request == nil {
		if len(s.pending) >= maxPendingRequests {
			s.mu.Unlock()
			return false
		}
		idBytes := make([]byte, 12)
		if _, err := rand.Read(idBytes); err != nil {
			s.mu.Unlock()
			return false
		}
		now := time.Now().UTC()
		request = &pendingRequest{
			request: model.PendingRequest{
				ID: hex.EncodeToString(idBytes), Origin: origin,
				RequestedAt: now, ExpiresAt: now.Add(requestTimeout),
			},
			done: make(chan struct{}),
		}
		s.pending[request.request.ID] = request
		s.pendingByOrigin[origin] = request
		s.addActivity("website", origin, "Website requested bridge access", "pending")
	}
	s.mu.Unlock()

	timer := time.NewTimer(time.Until(request.request.ExpiresAt))
	defer timer.Stop()
	select {
	case <-request.done:
		return request.allowed
	case <-timer.C:
		_, _, _ = s.ResolveRequest(request.request.ID, false, nil)
		return false
	}
}

func (s *Service) isBridgeLoopback(origin *url.URL) bool {
	host := origin.Hostname()
	_, port, err := net.SplitHostPort(s.webAddress)
	return err == nil && origin.Port() == port && (host == "127.0.0.1" || host == "localhost" || host == "::1")
}

func (s *Service) acceptsBrowserClient(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clientID != "" || s.browserClientID == ""
}

func (s *Service) browserSnapshot() *wsConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browser
}

func (s *Service) sendStatus(kind string, connected bool) {
	s.mu.Lock()
	sessions := append([]*browserSession(nil), s.browserSessions...)
	active := s.browser
	s.mu.Unlock()
	if len(sessions) == 0 && active != nil {
		s.sendStatusTo(active, kind, connected)
		return
	}
	for _, session := range sessions {
		s.sendStatusTo(session.ws, kind, connected)
	}
}

func (s *Service) sendStatusTo(ws *wsConn, kind string, connected bool) {
	if ws == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{"type": kind, "connected": connected})
	_ = ws.writeFrame(1, payload)
}

func (s *Service) attachBrowser(ws *wsConn, clientID, origin string) {
	s.attachBrowserWithReservation(ws, clientID, origin, false)
}

func (s *Service) attachBrowserWithReservation(ws *wsConn, clientID, origin string, reserved bool) {
	s.mu.Lock()
	if reserved && s.browserReservations > 0 {
		s.browserReservations--
	}
	for _, session := range s.browserSessions {
		if session.ws == ws {
			s.mu.Unlock()
			return
		}
	}
	session := &browserSession{ws: ws, clientID: clientID, origin: origin}
	s.browserSessions = append(s.browserSessions, session)
	previous := s.browser
	s.setActiveBrowserLocked(session)
	previousHostConnected := previous != nil && s.host != nil && s.hostBrowser == previous
	hostConnected := s.host != nil && s.hostBrowser == ws
	walletAllowed := s.walletAllowed
	s.mu.Unlock()
	if previous != nil && previous != ws {
		writeBrowserHello(previous, s.version, previousHostConnected, walletAllowed)
	}
	writeBrowserHello(ws, s.version, hostConnected, walletAllowed)
}

func writeBrowserHello(ws *wsConn, version string, hostConnected, walletAllowed bool) {
	hello, _ := json.Marshal(map[string]any{
		"type": "hello", "version": version, "hostConnected": hostConnected, "walletAllowed": walletAllowed,
	})
	_ = ws.writeFrame(1, hello)
}

func (s *Service) detachBrowser(ws *wsConn) {
	s.mu.Lock()
	wasActive := s.browser == ws
	var hostToClose net.Conn
	quarantined := false
	for index, session := range s.browserSessions {
		if session.ws == ws {
			s.browserSessions = append(s.browserSessions[:index], s.browserSessions[index+1:]...)
			break
		}
	}
	if wasActive {
		s.browser = nil
		s.browserOrigin = ""
		if count := len(s.browserSessions); count > 0 {
			s.setActiveBrowserLocked(s.browserSessions[count-1])
		}
	}
	if s.hostBrowser == ws {
		quarantined = s.quarantineHostBrowserLocked()
		hostToClose = s.host
		s.host = nil
		s.hostBrowser = nil
		s.hostBrowserClientID = ""
		s.clearHostTransactionLocked()
	}
	active := s.browser
	activeHostConnected := active != nil && s.host != nil && s.hostBrowser == active
	walletAllowed := s.walletAllowed
	s.mu.Unlock()
	_ = ws.conn.Close()
	if hostToClose != nil {
		_ = hostToClose.Close()
	}
	if quarantined {
		s.addActivity("request", "", "An interrupted HWI response requires a simulator page reload before retrying", "recovery-required")
	}
	if wasActive && active != nil {
		writeBrowserHello(active, s.version, activeHostConnected, walletAllowed)
	}
}

// setActiveBrowserLocked promotes a connected simulator session. The caller
// must hold s.mu.
func (s *Service) setActiveBrowserLocked(session *browserSession) {
	s.browser = session.ws
	s.browserOrigin = session.origin
	s.browserClientID = session.clientID
}

func (s *Service) handleBrowser(ws *wsConn, clientID, origin string) {
	s.handleBrowserWithHeartbeat(ws, clientID, origin, browserPingInterval, browserReadTimeout)
}

func (s *Service) handleBrowserWithHeartbeat(ws *wsConn, clientID, origin string, pingInterval, readTimeout time.Duration) {
	s.handleBrowserWithHeartbeatReservation(ws, clientID, origin, pingInterval, readTimeout, false)
}

func (s *Service) handleBrowserWithReservation(ws *wsConn, clientID, origin string) {
	s.handleBrowserWithHeartbeatReservation(ws, clientID, origin, browserPingInterval, browserReadTimeout, true)
}

func (s *Service) handleBrowserWithHeartbeatReservation(ws *wsConn, clientID, origin string, pingInterval, readTimeout time.Duration, reserved bool) {
	if pingInterval <= 0 {
		pingInterval = browserPingInterval
	}
	if readTimeout <= 0 {
		readTimeout = browserReadTimeout
	}
	if err := ws.conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		if reserved {
			s.releaseBrowserReservation()
		}
		_ = ws.conn.Close()
		return
	}
	s.attachBrowserWithReservation(ws, clientID, origin, reserved)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				if err := ws.writeFrame(9, []byte("specter-vhost")); err != nil {
					_ = ws.conn.Close()
					return
				}
			}
		}
	}()
	defer func() {
		close(stopHeartbeat)
		s.detachBrowser(ws)
		<-heartbeatDone
	}()
	for {
		opcode, payload, err := ws.readFrame()
		if err != nil {
			return
		}
		_ = ws.conn.SetReadDeadline(time.Now().Add(readTimeout))
		switch opcode {
		case 2:
			s.mu.Lock()
			host := s.host
			active := host != nil && s.hostBrowser == ws
			responseComplete := false
			responseBytes := 0
			requestID := s.hostRequestID
			command := s.hostCommand
			if active && s.hostRequestStarted && s.hostResponseLines < 2 {
				for _, value := range payload {
					if value == '\n' {
						s.hostResponseLines++
					}
				}
				s.hostResponseBytes += len(payload)
				responseBytes = s.hostResponseBytes
				if s.hostResponseLines >= 2 {
					s.hostResponseLines = 2
					responseComplete = true
				}
			}
			s.mu.Unlock()
			if !active {
				continue
			}
			if host != nil {
				_ = host.SetWriteDeadline(time.Now().Add(hostResponseWriteTimeout))
				err := writeFull(host, payload)
				_ = host.SetWriteDeadline(time.Time{})
				if err != nil {
					log.Printf("Unable to write firmware response to host application: %v", err)
					_ = host.Close()
					continue
				}
				if responseComplete {
					_ = host.SetReadDeadline(time.Now().Add(s.hostRequestTimeout()))
					message := fmt.Sprintf("%s %s response returned (%d bytes)", requestID, command, responseBytes)
					s.addActivity("response", origin, message, "returned")
				}
			}
		}
	}
}

func (s *Service) acceptHosts(listener net.Listener, generation uint64) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if !s.running || s.generation != generation || s.hwiListener != listener {
			s.mu.Unlock()
			_ = connection.Close()
			continue
		}
		if len(s.hostConnections) >= maxHostConnections {
			s.mu.Unlock()
			_ = connection.Close()
			log.Printf("Rejecting HWI client: bridge already has %d accepted HWI connections", maxHostConnections)
			continue
		}
		if s.hostConnections == nil {
			s.hostConnections = make(map[net.Conn]struct{})
		}
		s.hostConnections[connection] = struct{}{}
		s.mu.Unlock()
		go s.handleHost(connection, generation)
	}
}

func (s *Service) handleHost(connection net.Conn, generation uint64) {
	// The simulator relay has no per-request IDs, so responses from the browser
	// can only be routed to one HWI socket at a time. Queue concurrent HWI
	// clients instead of replacing and closing the socket currently in flight.
	s.hostSessionMu.Lock()
	defer s.hostSessionMu.Unlock()

	s.mu.Lock()
	registered := false
	if _, exists := s.hostConnections[connection]; exists {
		registered = true
	}
	if !s.running || s.generation != generation || !registered || !s.walletAllowed || s.browser == nil {
		allowed := s.walletAllowed
		delete(s.hostConnections, connection)
		s.mu.Unlock()
		_ = connection.Close()
		if !allowed {
			s.addActivity("wallet", "", "Wallet connection rejected", "blocked")
		}
		return
	}
	ws := s.browser
	origin := s.browserOrigin
	clientID := s.browserClientID
	if s.browserQuarantinedLocked(ws, clientID) {
		delete(s.hostConnections, connection)
		s.mu.Unlock()
		_ = connection.Close()
		return
	}
	s.host = connection
	s.hostBrowser = ws
	s.hostBrowserClientID = clientID
	s.clearHostTransactionLocked()
	s.mu.Unlock()
	s.sendStatusTo(ws, "host", true)
	s.addActivity("wallet", "", "Wallet application connected", "connected")
	defer func() {
		s.mu.Lock()
		wasCurrent := s.host == connection
		quarantined := false
		requestID := s.hostRequestID
		command := s.hostCommand
		responseLines := s.hostResponseLines
		responseBytes := s.hostResponseBytes
		if wasCurrent {
			quarantined = s.quarantineHostBrowserLocked()
			s.host = nil
			s.hostBrowser = nil
			s.hostBrowserClientID = ""
			s.clearHostTransactionLocked()
		}
		delete(s.hostConnections, connection)
		s.mu.Unlock()
		_ = connection.Close()
		if wasCurrent {
			s.sendStatusTo(ws, "host", false)
		}
		if quarantined {
			message := fmt.Sprintf("HWI response interrupted after %d response bytes across %d line(s); reload the simulator page before retrying", responseBytes, responseLines)
			if requestID != "" {
				message = fmt.Sprintf("%s %s response interrupted after %d response bytes across %d line(s); reload the simulator page before retrying", requestID, command, responseBytes, responseLines)
			}
			s.addActivity("request", origin, message, "recovery-required")
		}
	}()

	buffer := make([]byte, 32*1024)
	commandLogged := false
	var commandPrefix []byte
	_ = connection.SetReadDeadline(time.Now().Add(s.hostRequestTimeout()))
	for {
		read, err := connection.Read(buffer)
		if read > 0 {
			_ = connection.SetReadDeadline(time.Time{})
			if commandLogged {
				s.mu.Lock()
				transactionComplete := s.host == connection && s.hostRequestStarted && s.hostResponseLines >= 2
				if transactionComplete {
					s.clearHostTransactionLocked()
				}
				s.mu.Unlock()
				if transactionComplete {
					commandLogged = false
					commandPrefix = nil
				}
			}
			var command string
			var requestID string
			if !commandLogged {
				commandPrefix = append(commandPrefix, buffer[:min(read, 256-len(commandPrefix))]...)
				if command = classifyCommand(commandPrefix); command != "" {
					commandLogged = true
					s.mu.Lock()
					if s.host == connection {
						s.hostSequence++
						s.hostRequestID = fmt.Sprintf("hwi-%06d", s.hostSequence)
						s.hostCommand = command
						s.hostRequestStarted = true
						s.hostResponseLines = 0
						s.hostResponseBytes = 0
					}
					requestID = s.hostRequestID
					s.mu.Unlock()
				}
			}
			if ws == nil {
				return
			}
			if err := ws.writeFrame(2, buffer[:read]); err != nil {
				log.Printf("Unable to forward HWI request to simulator: %v", err)
				return
			}
			if requestID != "" {
				s.addActivity("request", origin, requestID+" "+command+" request forwarded", "forwarded")
			}
		}
		if err != nil {
			return
		}
	}
}

func classifyCommand(payload []byte) string {
	lines := strings.Split(string(payload), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		command := strings.Fields(line)[0]
		switch command {
		case "sign", "signmessage", "fingerprint", "xpub", "showaddr", "getrandom", "addwallet":
			return command
		default:
			return "USB"
		}
	}
	return ""
}

func (s *Service) addActivity(kind, origin, message, result string) {
	if s.activity != nil {
		s.activity(kind, origin, message, result)
	}
}

func openBrowser(target string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		command = exec.Command("open", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	return command.Start()
}
