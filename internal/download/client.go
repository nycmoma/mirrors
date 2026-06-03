package download

import (
	"context"
	"net/http"
	"time"

	"mirrors/internal/logging"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultRetries    = 3
	defaultRetryDelay = time.Second
)

// Client downloads repository metadata and package files.
type Client struct {
	httpClient *http.Client
	retries    int
	retryDelay time.Duration
	sleep      func(context.Context, time.Duration) error
	logger     logging.Logger
}

// Downloader is the testable download interface used by later workflow phases.
type Downloader interface {
	FetchMetadata(ctx context.Context, rawURL string, expected *Checksum) ([]byte, error)
	DownloadPackage(ctx context.Context, rawURL, destination string, expected *Checksum) error
	GetLength(ctx context.Context, rawURL string) (int64, error)
}

// Option configures a Client.
type Option func(*Client)

// NewClient creates a downloader client.
func NewClient(options ...Option) *Client {
	client := &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		retries:    defaultRetries,
		retryDelay: defaultRetryDelay,
		sleep:      sleepContext,
		logger:     logging.Nop(),
	}

	for _, option := range options {
		option(client)
	}
	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if client.sleep == nil {
		client.sleep = sleepContext
	}
	if client.logger == nil {
		client.logger = logging.Nop()
	}
	if client.retries < 0 {
		client.retries = 0
	}

	return client
}

// WithHTTPClient replaces the HTTP client, mainly for tests.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.httpClient = httpClient
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(client *Client) {
		if client.httpClient == nil {
			client.httpClient = &http.Client{}
		}
		client.httpClient.Timeout = timeout
	}
}

// WithRetries sets retry count after the first attempt.
func WithRetries(retries int) Option {
	return func(client *Client) {
		client.retries = retries
	}
}

// WithRetryDelay sets the delay before each retry.
func WithRetryDelay(delay time.Duration) Option {
	return func(client *Client) {
		client.retryDelay = delay
	}
}

// WithSleeper replaces retry sleeping, mainly for deterministic tests.
func WithSleeper(sleeper func(context.Context, time.Duration) error) Option {
	return func(client *Client) {
		client.sleep = sleeper
	}
}

// WithLogger sets a diagnostic logger for retry events.
func WithLogger(logger logging.Logger) Option {
	return func(client *Client) {
		client.logger = logger
	}
}

func (client *Client) doWithRetry(ctx context.Context, operation func() error) error {
	var err error
	attempts := client.retries + 1
	delay := client.retryDelay

	for attempt := 0; attempt < attempts; attempt++ {
		err = operation()
		if err == nil {
			return nil
		}
		if attempt == attempts-1 || !retryable(err) {
			return err
		}
		client.logger.Debugf("download retry attempt=%d next_attempt=%d max_attempts=%d delay=%s error=%v", attempt+1, attempt+2, attempts, delay, err)
		if sleepErr := client.sleep(ctx, delay); sleepErr != nil {
			return sleepErr
		}
		if delay > 0 {
			delay *= 2
		}
	}

	return err
}
