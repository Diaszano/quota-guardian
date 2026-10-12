package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

const maxInput = 1 << 20

type ObservationClient interface {
	Observe(context.Context, ports.Event) error
}

func Handle(ctx context.Context, provider string, in io.Reader, out io.Writer, client ObservationClient) error {
	deadline, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	response := neutralResponse(provider)
	defer func() {
		if out != nil {
			_, _ = io.WriteString(out, response)
		}
	}()
	result := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(in, maxInput+1))
		result <- struct {
			data []byte
			err  error
		}{data, err}
	}()
	var payload []byte
	select {
	case <-deadline.Done():
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil
	case read := <-result:
		if read.err != nil || len(read.data) > maxInput {
			return nil
		}
		payload = read.data
	}
	event, err := normalize(provider, payload)
	if err != nil || client == nil {
		return nil
	}
	done := make(chan struct{}, 1)
	go func() { _ = client.Observe(deadline, event); done <- struct{}{} }()
	select {
	case <-deadline.Done():
	case <-done:
	}
	return nil
}

func neutralResponse(provider string) string {
	if strings.HasPrefix(provider, "agy/") {
		switch strings.TrimPrefix(provider, "agy/") {
		case "Stop":
			return `{"decision":"allow"}` + "\n"
		case "PreInvocation":
			return `{"injectSteps":[]}` + "\n"
		case "PostInvocation":
			return `{"injectSteps":[],"terminationBehavior":""}` + "\n"
		}
	}
	return "{}\n"
}

func decodePayload(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing hook data")
	}
	return nil
}
