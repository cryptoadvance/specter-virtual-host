package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrNotRunning = errors.New("Specter Virtual Host is not running")

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) Execute(ctx context.Context, command string, arguments map[string]string) (any, error) {
	connection, err := dial(ctx, 400*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	defer connection.Close()
	if err := json.NewEncoder(connection).Encode(Request{Command: command, Arguments: arguments}); err != nil {
		return nil, err
	}
	var response Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, errors.New(response.Error)
	}
	var result any
	if len(response.Data) > 0 {
		if err := json.Unmarshal(response.Data, &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func Available(ctx context.Context) bool {
	connection, err := dial(ctx, 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}
