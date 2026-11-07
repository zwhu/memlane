package serviceipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// Call opens one private IPC connection, with caller cancellation affecting
// both dialing and reads. It never opens the business database.
func Call(ctx context.Context, root string, request Request, out any) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute service root required")
	}
	request.Protocol = 1
	if request.ID == "" {
		request.ID = uuid.NewString()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", SocketPath(root))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = SameUser(conn); err != nil {
		return err
	}
	deadline := time.Now().Add(120 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-closed:
		}
	}()
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	var response struct {
		Protocol int             `json:"protocol"`
		ID       string          `json:"id"`
		OK       bool            `json:"ok"`
		Result   json.RawMessage `json:"result"`
		Error    string          `json:"error"`
	}
	if err = json.NewDecoder(conn).Decode(&response); err != nil {
		return err
	}
	if response.Protocol != 1 || response.ID != request.ID {
		return errors.New("service protocol mismatch")
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	if out != nil {
		return json.Unmarshal(response.Result, out)
	}
	return nil
}

func IsStopped(root string) (bool, error) {
	if !filepath.IsAbs(root) {
		return false, errors.New("absolute service root required")
	}
	data, err := os.ReadFile(filepath.Join(root, "service-state.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state State
	if err = json.Unmarshal(data, &state); err != nil {
		return false, err
	}
	return state.Stopped, nil
}
