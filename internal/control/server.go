package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/cryptoadvance/specter-virtual-host/internal/core"
)

type Server struct {
	mu       sync.Mutex
	listener net.Listener
	executor core.Executor
}

func StartServer(executor core.Executor) (*Server, error) {
	listener, err := listen()
	if err != nil {
		return nil, err
	}
	server := &Server{listener: listener, executor: executor}
	go server.accept(listener)
	return server, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.listener = nil
	cleanupEndpoint()
	return err
}

func (s *Server) accept(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		go s.handle(connection)
	}
}

func (s *Server) handle(connection net.Conn) {
	defer connection.Close()
	decoder := json.NewDecoder(bufio.NewReader(io.LimitReader(connection, 1<<20)))
	encoder := json.NewEncoder(connection)
	var request Request
	if err := decoder.Decode(&request); err != nil {
		_ = encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}
	data, err := s.executor.Execute(context.Background(), request.Command, request.Arguments)
	if err != nil {
		_ = encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		_ = encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}
	_ = encoder.Encode(Response{OK: true, Data: encoded})
}

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrNotRunning)
}
