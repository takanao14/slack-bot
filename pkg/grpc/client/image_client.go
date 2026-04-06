package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	imagev1 "github.com/takanao14/led-image-api/gen/go/image/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ImageClient is a gRPC client for the LED image service.
type ImageClient struct {
	conn    *grpc.ClientConn
	client  imagev1.ImageServiceClient
	logger  *slog.Logger
	timeout time.Duration
}

// NewImageClient creates a new ImageClient and connects to the gRPC server.
func NewImageClient(addr string, connectTimeout, opTimeout time.Duration, logger *slog.Logger) (*ImageClient, error) {
	// Service config for retries
	// See: https://github.com/grpc/grpc/blob/master/doc/service_config.md
	serviceConfig := `{
		"methodConfig": [{
			"name": [{"service": "image.v1.ImageService"}],
			"retryPolicy": {
				"maxAttempts": 3,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2,
				"retryableStatusCodes": ["UNAVAILABLE", "INTERNAL"]
			}
		}]
	}`

	dialOptions := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithBlock(), // Block until the connection is established or fails
	}
	dialOptions = append(dialOptions, additionalDialOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server within timeout at %s: %w", addr, err)
	}

	logger.Info("Successfully connected to gRPC server", slog.String("addr", addr))

	return &ImageClient{
		conn:    conn,
		client:  imagev1.NewImageServiceClient(conn),
		logger:  logger,
		timeout: opTimeout,
	}, nil
}

// additionalDialOptions is a hook for injecting extra dial options, primarily for testing.
var additionalDialOptions = func() []grpc.DialOption {
	return nil
}

// SendImage sends image data to the LED display service.
func (c *ImageClient) SendImage(ctx context.Context, imageData []byte, mimeType string, durationSeconds int32) (*imagev1.SendImageResponse, error) {
	// Apply the operation timeout to the context.
	opCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req := &imagev1.SendImageRequest{
		Image: &imagev1.ImageData{
			ImageData: imageData,
			MimeType:  mimeType,
		},
		DurationSeconds: durationSeconds,
	}

	c.logger.Debug("Sending image to LED display",
		slog.String("mime_type", mimeType),
		slog.Int("size", len(imageData)),
		slog.Int("duration", int(durationSeconds)),
	)

	resp, err := c.client.SendImage(opCtx, req)
	if err != nil {
		c.logger.Error("Failed to send image via gRPC",
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("gRPC SendImage failed: %w", err)
	}

	if !resp.Success {
		c.logger.Warn("Image send was not successful",
			slog.String("message", resp.Message),
		)
		return resp, fmt.Errorf("image send failed on server: %s", resp.Message)
	}

	c.logger.Info("Image sent successfully",
		slog.String("message", resp.Message),
	)

	return resp, nil
}

// Close closes the underlying gRPC connection.
func (c *ImageClient) Close() error {
	if c.conn != nil {
		if c.logger != nil {
			c.logger.Info("Closing gRPC connection")
		}
		return c.conn.Close()
	}
	return nil
}
