package bridge

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const maxFrame = 16 << 20
const writeTimeout = 15 * time.Second

type wsConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (ws *wsConn) writeFrame(opcode byte, payload []byte) error {
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()

	deadline := time.Now().Add(writeTimeout)
	switch opcode {
	case websocket.CloseMessage, websocket.PingMessage, websocket.PongMessage:
		return ws.conn.WriteControl(int(opcode), payload, deadline)
	case websocket.TextMessage, websocket.BinaryMessage:
		if err := ws.conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() { _ = ws.conn.SetWriteDeadline(time.Time{}) }()
		return ws.conn.WriteMessage(int(opcode), payload)
	default:
		return errors.New("unsupported WebSocket message type")
	}
}

func (ws *wsConn) readFrame() (byte, []byte, error) {
	opcode, payload, err := ws.conn.ReadMessage()
	return byte(opcode), payload, err
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request, checkOrigin func(*http.Request) bool) (*wsConn, error) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     checkOrigin,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxFrame)
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeTimeout))
	})
	return &wsConn{conn: conn}, nil
}
