package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/outbound"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	awsv4 "github.com/aws/aws-sdk-go/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

const (
	// s3MultipartThreshold 以上的对象改走分片上传。
	// 单次 PutObject 会把整个对象塞进一个 HTTP 请求体，在 Cloudflare 这类前置代理
	// （默认请求体上限 100MiB）后面会被 413 直接拒绝，大视频因此永远写不进去。
	s3MultipartThreshold = 32 << 20
	// 每片 8MiB：远低于 100MiB 上限；片小一点在隧道上更容易并行摊平 RTT。
	s3MultipartPartSize = 8 << 20
	// 6 路并发：实测单流只有 ~0.5MB/s，靠多片并行把链路跑满。
	s3MultipartConcurrency = 6
)

func ValidateStorageEndpoint(raw string) (*url.URL, error) {
	parsed, err := outbound.ValidateOutboundURL(raw)
	if err != nil {
		return nil, err
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Trim(parsed.Path, "/") != "" {
		return nil, kernel.BadAuthRequest("对象存储 Endpoint 必须是服务根 URL，不能包含认证信息、路径、查询参数或片段")
	}
	if parsed.Scheme == "http" && !outbound.AllowedPrivateUpstreamHost(parsed.Hostname()) {
		return nil, kernel.BadAuthRequest("对象存储 HTTP Endpoint 仅允许访问 CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS 精确放行的主机")
	}
	return parsed, nil
}

func StandardAWSS3Endpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "s3.amazonaws.com" || strings.HasPrefix(host, "s3.") && (strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".amazonaws.com.cn")) || strings.HasPrefix(host, "s3-") && strings.HasSuffix(host, ".amazonaws.com")
}

func PublicHTTPSStorageEndpoint(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	return outbound.ValidateOutboundHost(parsed.Hostname()) == nil && !outbound.AllowedPrivateUpstreamHost(parsed.Hostname())
}

func NewS3Client(setting Settings, timeout time.Duration) (*awss3.S3, error) {
	setting = NormalizeSettings(setting)
	endpoint, err := ValidateStorageEndpoint(setting.Endpoint)
	if err != nil {
		return nil, err
	}
	if setting.Region == "" || setting.Bucket == "" || setting.AccessKeyID == "" || setting.AccessKeySecret == "" {
		return nil, errors.New("S3 Region、Bucket 或访问密钥不完整")
	}
	httpClient := outbound.OutboundHTTPClient(timeout)
	config := aws.NewConfig().
		WithRegion(setting.Region).
		WithEndpoint(endpoint.String()).
		WithCredentials(credentials.NewStaticCredentials(setting.AccessKeyID, setting.AccessKeySecret, setting.SessionToken)).
		WithHTTPClient(httpClient).
		WithS3ForcePathStyle(setting.PathStyle || !StandardAWSS3Endpoint(endpoint.String())).
		WithDisableSSL(endpoint.Scheme == "http")
	sess, err := session.NewSession(config)
	if err != nil {
		return nil, err
	}
	return awss3.New(sess), nil
}

func PutS3Object(setting Settings, objectKey string, mimeType string, size int64, body io.Reader) (string, error) {
	return putS3Object(context.Background(), setting, objectKey, mimeType, size, body)
}

func putS3Object(ctx context.Context, setting Settings, objectKey string, mimeType string, size int64, body io.Reader) (string, error) {
	setting = NormalizeSettings(setting)
	client, err := NewS3Client(setting, transferTimeout(setting))
	if err != nil {
		return "", err
	}
	key := strings.TrimLeft(objectKey, "/")
	if size < 0 || size >= s3MultipartThreshold {
		// 大对象不再整体缓冲，直接按片流式上传：既绕开代理请求体上限，
		// 也避免把几百 MB 视频读进内存。
		return putS3ObjectMultipart(ctx, client, setting, key, mimeType, size, body)
	}
	var seekable io.ReadSeeker
	if reader, ok := body.(io.ReadSeeker); ok {
		seekable = reader
	} else {
		limit := maxBufferedUploadSize(setting) + 1
		if size >= 0 && size < limit {
			limit = size + 1
		}
		data, readErr := io.ReadAll(io.LimitReader(body, limit))
		if readErr != nil {
			return "", readErr
		}
		if size >= 0 && int64(len(data)) != size {
			return "", errors.New("S3 上传内容长度与资源记录不一致")
		}
		seekable = bytes.NewReader(data)
	}
	input := &awss3.PutObjectInput{Bucket: aws.String(setting.Bucket), Key: aws.String(key), Body: seekable}
	if mimeType != "" {
		input.ContentType = aws.String(mimeType)
	}
	if size >= 0 {
		input.ContentLength = aws.Int64(size)
	}
	output, err := client.PutObjectWithContext(ctx, input)
	if err != nil {
		return "", fmt.Errorf("S3 上传失败：%w", err)
	}
	return strings.Trim(aws.StringValue(output.ETag), `"`), nil
}

// putS3ObjectMultipart 用分片上传写入大对象。
// 相较单次 PutObject：每个分片是独立 HTTP 请求（16MiB），不会被前置代理的
// 请求体上限拦截；ETag 直接取自 CompleteMultipartUpload，大小校验尽力而为。
func putS3ObjectMultipart(ctx context.Context, client *awss3.S3, setting Settings, key string, mimeType string, size int64, body io.Reader) (string, error) {
	reader := body
	if size >= 0 {
		// 用最多 size 字节喂给分片器：既保证不会读到资源记录之外的内容，
		// 也让分片器在正确位置收到 EOF。
		reader = io.LimitReader(body, size)
	}
	uploader := s3manager.NewUploaderWithClient(client, func(u *s3manager.Uploader) {
		u.PartSize = s3MultipartPartSize
		u.Concurrency = s3MultipartConcurrency
		u.LeavePartsOnError = false
	})
	input := &s3manager.UploadInput{Bucket: aws.String(setting.Bucket), Key: aws.String(key), Body: reader}
	if mimeType != "" {
		input.ContentType = aws.String(mimeType)
	}
	output, err := uploader.UploadWithContext(ctx, input)
	if err != nil {
		return "", fmt.Errorf("S3 分片上传失败：%w", err)
	}
	// CompleteMultipartUpload 已经把 ETag 带回来了，不必为了取 ETag 再发一次 HEAD。
	etag := strings.Trim(aws.StringValue(output.ETag), `"`)
	if size >= 0 {
		// 尽力校验落盘大小。部分 S3 兼容网关对 HEAD 的鉴权与 GET 不一致，
		// 这种情况不应该让已经成功的上传变成失败。
		if head, headErr := client.HeadObjectWithContext(ctx, &awss3.HeadObjectInput{Bucket: aws.String(setting.Bucket), Key: aws.String(key)}); headErr == nil {
			if got := aws.Int64Value(head.ContentLength); got != size {
				return "", fmt.Errorf("S3 分片上传后大小不一致：期望 %d，实际 %d", size, got)
			}
			if etag == "" {
				etag = strings.Trim(aws.StringValue(head.ETag), `"`)
			}
		}
	}
	return etag, nil
}

func GetS3ObjectRange(setting Settings, objectKey string, rangeHeader string) (*ObjectStream, error) {
	setting = NormalizeSettings(setting)
	client, err := NewS3Client(setting, transferTimeout(setting))
	if err != nil {
		return nil, err
	}
	input := &awss3.GetObjectInput{Bucket: aws.String(setting.Bucket), Key: aws.String(strings.TrimLeft(objectKey, "/"))}
	if rangeHeader != "" {
		input.Range = aws.String(rangeHeader)
	}
	output, err := client.GetObjectWithContext(context.Background(), input)
	if err != nil {
		if requestFailure, ok := err.(awserr.RequestFailure); ok && requestFailure.StatusCode() == http.StatusRequestedRangeNotSatisfiable {
			return &ObjectStream{Body: io.NopCloser(bytes.NewReader(nil)), StatusCode: http.StatusRequestedRangeNotSatisfiable, AcceptRanges: "bytes"}, nil
		}
		return nil, fmt.Errorf("S3 读取失败：%w", err)
	}
	status := http.StatusOK
	if rangeHeader != "" && aws.StringValue(output.ContentRange) != "" {
		status = http.StatusPartialContent
	}
	return &ObjectStream{Body: output.Body, StatusCode: status, ContentLength: aws.Int64Value(output.ContentLength), ContentRange: aws.StringValue(output.ContentRange), AcceptRanges: kernel.FirstNonEmpty(aws.StringValue(output.AcceptRanges), "bytes")}, nil
}

func SignedS3ObjectURL(setting Settings, objectKey string, expiresAt time.Time) (string, error) {
	return signedS3ObjectURL(setting, objectKey, objectURLSigning{ExpiresAt: expiresAt})
}

func signedS3ObjectURL(setting Settings, objectKey string, signing objectURLSigning) (string, error) {
	setting = NormalizeSettings(setting)
	client, err := NewS3Client(setting, transferTimeout(setting))
	if err != nil {
		return "", err
	}
	if !signing.ExpiresAt.After(time.Now()) {
		return "", errors.New("S3 签名有效期必须晚于当前时间")
	}
	input := &awss3.GetObjectInput{Bucket: aws.String(setting.Bucket), Key: aws.String(strings.TrimLeft(objectKey, "/"))}
	if signing.CacheControl != "" {
		input.ResponseCacheControl = aws.String(signing.CacheControl)
	}
	if signing.ContentDisposition != "" {
		input.ResponseContentDisposition = aws.String(signing.ContentDisposition)
	}
	req, _ := client.GetObjectRequest(input)
	signedAt := signingTime(signing.SignedAt)
	// SDK 默认以 time.Now 作为 X-Amz-Date；替换为对齐后的签名时间，保证同一窗口内地址稳定。
	// DisableURIPathEscaping 与 SDK 为 S3 注册的默认签名器保持一致。
	req.Handlers.Sign.Swap(awsv4.SignRequestHandler.Name, request.NamedHandler{Name: awsv4.SignRequestHandler.Name, Fn: func(r *request.Request) {
		awsv4.SignSDKRequestWithCurrentTime(r, func() time.Time { return signedAt }, func(s *awsv4.Signer) { s.DisableURIPathEscaping = true })
	}})
	value, err := req.Presign(signing.ExpiresAt.Sub(signedAt))
	if err != nil {
		return "", fmt.Errorf("S3 下载地址签名失败：%w", err)
	}
	return value, nil
}

func DeleteS3Object(setting Settings, objectKey string) error {
	setting = NormalizeSettings(setting)
	client, err := NewS3Client(setting, transferTimeout(setting))
	if err != nil {
		return err
	}
	_, err = client.DeleteObjectWithContext(context.Background(), &awss3.DeleteObjectInput{Bucket: aws.String(setting.Bucket), Key: aws.String(strings.TrimLeft(objectKey, "/"))})
	if requestFailure, ok := err.(awserr.RequestFailure); ok && requestFailure.StatusCode() == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("删除 S3 对象失败：%w", err)
	}
	return nil
}
