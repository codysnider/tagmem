package daemon

import (
	"encoding/json"
	"fmt"
	"io"
)

type CorpusDocumentPayload struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Mode      string `json:"mode,omitempty"`
	Extract   string `json:"extract,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type EnsureCorpusPayload struct {
	Key       string                  `json:"key"`
	Documents []CorpusDocumentPayload `json:"documents"`
}

type SearchCorpusPayload struct {
	Key   string `json:"key"`
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

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

func DecodePayload(payload map[string]any, target any) error {
	if payload == nil {
		return nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode payload: %w", err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}
