package daemon

import (
	"encoding/json"
	"fmt"
	"io"
)

type Request struct {
	ID      string         `json:"id"`
	Command string         `json:"command"`
	Payload map[string]any `json:"payload,omitempty"`
}

type Response struct {
	ID      string         `json:"id"`
	Success bool           `json:"success"`
	Payload map[string]any `json:"payload,omitempty"`
	Error   string         `json:"error,omitempty"`
}

func WriteRequest(w io.Writer, request Request) error {
	if err := json.NewEncoder(w).Encode(request); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	return nil
}

func ReadRequest(r io.Reader) (Request, error) {
	var request Request
	if err := json.NewDecoder(r).Decode(&request); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}

	return request, nil
}

func WriteResponse(w io.Writer, response Response) error {
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}

	return nil
}

func ReadResponse(r io.Reader) (Response, error) {
	var response Response
	if err := json.NewDecoder(r).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}

	return response, nil
}
