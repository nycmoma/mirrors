package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// FetchMetadata downloads metadata bytes and optionally verifies them.
func (client *Client) FetchMetadata(ctx context.Context, rawURL string, expected *Checksum) ([]byte, error) {
	var data []byte

	err := client.doWithRetry(ctx, func() error {
		body, err := client.get(ctx, rawURL)
		if err != nil {
			return err
		}
		defer body.Close()

		checksummer := newChecksumWriter()
		var buffer bytes.Buffer
		_, err = io.Copy(io.MultiWriter(&buffer, checksummer), body)
		if err != nil {
			return err
		}

		if err := verifyChecksum(rawURL, checksummer.Sum(), expected); err != nil {
			return verificationError{err: err}
		}

		data = buffer.Bytes()
		return nil
	})
	if err != nil {
		return nil, err
	}

	return data, nil
}

// DownloadPackage downloads a package or metadata file to destination.
func (client *Client) DownloadPackage(ctx context.Context, rawURL, destination string, expected *Checksum) error {
	return client.DownloadPackageWithProgress(ctx, rawURL, destination, expected, nil)
}

// DownloadPackageWithProgress downloads a package and reports current bytes for each attempt.
func (client *Client) DownloadPackageWithProgress(ctx context.Context, rawURL, destination string, expected *Checksum, onBytes func(int64)) error {
	var tempPath string

	err := client.doWithRetry(ctx, func() error {
		var err error
		tempPath, err = client.downloadOnce(ctx, rawURL, destination, expected, onBytes)
		return err
	})
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return err
	}

	if err := os.Rename(tempPath, destination); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("%s: rename downloaded file: %w", rawURL, err)
	}

	return nil
}

// GetLength returns the Content-Length for rawURL.
func (client *Client) GetLength(ctx context.Context, rawURL string) (int64, error) {
	var length int64

	err := client.doWithRetry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
		if err != nil {
			return err
		}

		resp, err := client.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
		}
		if resp.ContentLength < 0 {
			return fmt.Errorf("%s: missing Content-Length", rawURL)
		}

		length = resp.ContentLength
		return nil
	})
	if err != nil {
		return -1, err
	}

	return length, nil
}

func (client *Client) get(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
	}

	return resp.Body, nil
}

func (client *Client) downloadOnce(ctx context.Context, rawURL, destination string, expected *Checksum, onBytes func(int64)) (string, error) {
	body, err := client.get(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer body.Close()

	if err := os.MkdirAll(filepath.Dir(destination), 0777); err != nil {
		return "", fmt.Errorf("%s: create destination directory: %w", rawURL, err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".*.down")
	if err != nil {
		return "", fmt.Errorf("%s: create temporary download file: %w", rawURL, err)
	}
	tempPath := tempFile.Name()

	checksummer := newChecksumWriter()
	writers := []io.Writer{tempFile, checksummer}
	if onBytes != nil {
		writers = append(writers, &progressWriter{onBytes: onBytes})
	}
	_, copyErr := io.Copy(io.MultiWriter(writers...), body)
	closeErr := tempFile.Close()
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tempPath)
		return "", closeErr
	}

	if err := verifyChecksum(rawURL, checksummer.Sum(), expected); err != nil {
		_ = os.Remove(tempPath)
		return "", verificationError{err: err}
	}

	return tempPath, nil
}

type progressWriter struct {
	onBytes func(int64)
	current int64
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	writer.current += int64(len(data))
	writer.onBytes(writer.current)
	return len(data), nil
}
