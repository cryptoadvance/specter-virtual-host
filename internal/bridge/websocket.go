package bridge

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxFrame = 16 << 20
const writeTimeout = 15 * time.Second

type wsConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

func (ws *wsConn) writeFrame(opcode byte, payload []byte) error {
	ws.writeMu.Lock()
	defer ws.writeMu.Unlock()
	if err := ws.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	defer func() { _ = ws.conn.SetWriteDeadline(time.Time{}) }()
	header := []byte{0x80 | opcode}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 0xffff:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127)
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(payload)))
		header = append(header, size[:]...)
	}
	if err := writeFull(ws.conn, header); err != nil {
		return err
	}
	return writeFull(ws.conn, payload)
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

func (ws *wsConn) readFrame() (byte, []byte, error) {
	first, err := ws.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	second, err := ws.reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if first&0x80 == 0 {
		return 0, nil, errors.New("fragmented WebSocket frames are not supported")
	}
	opcode := first & 0x0f
	masked := second&0x80 != 0
	if !masked {
		return 0, nil, errors.New("unmasked client frame")
	}
	length := uint64(second & 0x7f)
	if length == 126 {
		var size [2]byte
		if _, err := io.ReadFull(ws.reader, size[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(size[:]))
	} else if length == 127 {
		var size [8]byte
		if _, err := io.ReadFull(ws.reader, size[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(size[:])
	}
	if length > maxFrame {
		return 0, nil, fmt.Errorf("WebSocket frame too large: %d bytes", length)
	}
	var mask [4]byte
	if _, err := io.ReadFull(ws.reader, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(ws.reader, payload); err != nil {
		return 0, nil, err
	}
	for index := range payload {
		payload[index] ^= mask[index%4]
	}
	return opcode, payload, nil
}

func acceptWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("not a WebSocket upgrade")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("invalid WebSocket handshake")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("HTTP connection cannot be upgraded")
	}
	connection, readWriter, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(digest[:])
	_, err = fmt.Fprintf(readWriter, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	if err == nil {
		err = readWriter.Flush()
	}
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &wsConn{conn: connection, reader: readWriter.Reader}, nil
}
