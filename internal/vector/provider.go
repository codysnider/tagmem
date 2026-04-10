package vector

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	chromem "github.com/philippgille/chromem-go"

	"github.com/codysnider/tagmem/internal/xdg"
)

const (
	ProviderEmbedded     = "embedded"
	ProviderOpenAI       = "openai"
	ProviderEmbeddedHash = "embedded-hash"

	defaultOpenAIModel   = "nomic-embed-text"
	defaultEmbeddedModel = "bge-small-en-v1.5"
)

type Provider struct {
	Name        string
	IndexKey    string
	Description string
	Model       string
	BaseURL     string
	APIKey      string
	Func        chromem.EmbeddingFunc
	Batch       func(context.Context, []string) ([][]float32, error)
	Details     func() map[string]string
}

type DoctorReport struct {
	Provider            string
	Description         string
	Model               string
	BaseURL             string
	ExecutionDevice     string
	RuntimeLibrary      string
	Reachable           bool
	EmbeddingWorks      bool
	EmbeddingDimensions int
	Diagnosis           string
	Hint                string
	Error               string
}

func ProviderFromEnv(paths xdg.Paths) (Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(envOrDefault("TAGMEM_EMBED_PROVIDER", "", ProviderEmbedded)))

	switch providerName {
	case "", ProviderEmbedded, "local", "builtin":
		model := strings.TrimSpace(envOrDefault("TAGMEM_EMBED_MODEL", "", defaultEmbeddedModel))
		accel := strings.TrimSpace(envOrDefault("TAGMEM_EMBED_ACCEL", "", "auto"))
		return EmbeddedProvider(paths, model, accel)
	case ProviderEmbeddedHash, "hash":
		return EmbeddedHashProvider(), nil
	case ProviderOpenAI, "openai-compatible", "compat", "ollama":
		model := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_MODEL", "", envOrDefault("OPENAI_MODEL", "", defaultOpenAIModel)))
		baseURL := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_BASE_URL", "", envOrDefault("OPENAI_BASE_URL", "", envOrDefault("OLLAMA_HOST", "", ""))))
		apiKey := strings.TrimSpace(envOrDefault("TAGMEM_OPENAI_API_KEY", "", envOrDefault("OPENAI_API_KEY", "", "")))
		return OpenAICompatibleProvider(model, baseURL, apiKey), nil
	default:
		return Provider{}, fmt.Errorf("unsupported embedding provider %q", providerName)
	}
}

func OpenAICompatibleProvider(model, baseURL, apiKey string) Provider {
	baseURL = normalizeOpenAIBaseURL(baseURL)
	indexKey := ProviderOpenAI + "-" + sanitizeKey(model)
	description := fmt.Sprintf("openai-compatible model %s", model)
	if baseURL != "" {
		description = fmt.Sprintf("%s at %s", description, baseURL)
	}

	return Provider{
		Name:        ProviderOpenAI,
		IndexKey:    indexKey,
		Description: description,
		Model:       model,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Func:        chromem.NewEmbeddingFuncOpenAICompat(baseURL, apiKey, model, nil),
	}
}

func (p Provider) IndexPath(root string) string {
	return filepath.Join(root, p.IndexKey)
}

func envOrDefault(primary, alias, fallback string) string {
	if value, ok := os.LookupEnv(primary); ok && strings.TrimSpace(value) != "" {
		return value
	}
	if value, ok := os.LookupEnv(alias); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func sanitizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "default"
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '-', r == '_', r == '.', r == '/', r == ':':
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}

	key := strings.Trim(builder.String(), "-")
	if key == "" {
		return "default"
	}
	return key
}

func normalizeOpenAIBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/")
	}

	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1"
		return strings.TrimRight(parsed.String(), "/")
	}

	baseURL = strings.TrimRight(parsed.String(), "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL
	}
	return baseURL
}

func (p Provider) Doctor(ctx context.Context) DoctorReport {
	report := DoctorReport{
		Provider:    p.Name,
		Description: p.Description,
		Model:       p.Model,
		BaseURL:     p.BaseURL,
	}

	if p.Name == ProviderOpenAI {
		report.Reachable = openAIReachable(ctx, p.BaseURL, p.APIKey)
	}

	vector, err := p.Func(ctx, "tiered memory health check")
	if err != nil {
		report.Error = err.Error()
		report.Diagnosis, report.Hint = diagnoseDoctorError(p, report.Error)
		return report
	}

	report.EmbeddingWorks = true
	report.EmbeddingDimensions = len(vector)
	if p.Details != nil {
		details := p.Details()
		report.ExecutionDevice = details["execution_device"]
		report.RuntimeLibrary = details["runtime_library"]
	}
	if p.Name != ProviderOpenAI {
		report.Reachable = true
	}

	return report
}

func openAIReachable(ctx context.Context, baseURL, apiKey string) bool {
	if baseURL == "" {
		baseURL = normalizeOpenAIBaseURL("http://localhost:11434")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return false
	}
	if strings.TrimSpace(apiKey) != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()

	return response.StatusCode >= 200 && response.StatusCode < 300
}

func diagnoseDoctorError(provider Provider, raw string) (string, string) {
	errorText := strings.ToLower(strings.TrimSpace(raw))

	switch {
	case strings.Contains(errorText, "no embeddings found in the response"):
		return "endpoint is reachable, but the configured model is not returning embeddings", "Serve a dedicated embeddings model on this endpoint, such as BAAI/bge-small-en-v1.5, then point TAGMEM_OPENAI_MODEL at that model."
	case strings.Contains(errorText, "connection refused"), strings.Contains(errorText, "no such host"), strings.Contains(errorText, "i/o timeout"), strings.Contains(errorText, "context deadline exceeded"):
		return "embedding endpoint is unreachable", "Confirm the host, port, and network path, and ensure the service is listening on a non-localhost interface."
	case strings.Contains(errorText, "401"), strings.Contains(errorText, "403"), strings.Contains(errorText, "unauthorized"), strings.Contains(errorText, "forbidden"):
		return "embedding endpoint rejected authentication", "Set TAGMEM_OPENAI_API_KEY or OPENAI_API_KEY to a valid token for the endpoint."
	case provider.Name == ProviderOpenAI:
		return "openai-compatible embedding request failed", "Verify the endpoint supports POST /v1/embeddings for the configured model and returns OpenAI-style embedding payloads."
	default:
		return "embedding provider check failed", "Inspect the reported error and provider configuration."
	}
}
