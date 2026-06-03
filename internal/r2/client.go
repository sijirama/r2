package r2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"r2/internal/config"
)

// Client wraps the S3-compatible R2 API plus the Cloudflare REST API.
type Client struct {
	cfg      *config.Config
	s3       *s3.Client
	presign  *s3.PresignClient
	http     *http.Client
	domainMu sync.Mutex
	domains  map[string]bucketDomain // cache: bucket -> public domain info
}

type bucketDomain struct {
	fetched bool
	host    string // e.g. pub-xxxx.r2.dev or cdn.example.com; empty if private
}

// Bucket is a lightweight view of an R2 bucket.
type Bucket struct {
	Name    string `json:"name"`
	Created string `json:"created"`
}

// Object is a lightweight view of a stored object.
type Object struct {
	Key      string `json:"key"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// Listing is the result of listing a bucket at a given prefix.
type Listing struct {
	Prefixes []string `json:"prefixes"`
	Objects  []Object `json:"objects"`
}

// Link is a resolved URL for an object.
type Link struct {
	URL     string `json:"url"`
	Type    string `json:"type"`    // "public" or "presigned"
	Expires string `json:"expires"` // human note, empty for public
}

// New builds a Client from config.
func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	s3c := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.S3Endpoint())
		// R2 does not support virtual-hosted bucket style addressing.
		o.UsePathStyle = true
	})

	return &Client{
		cfg:     cfg,
		s3:      s3c,
		presign: s3.NewPresignClient(s3c),
		http:    &http.Client{Timeout: 15 * time.Second},
		domains: map[string]bucketDomain{},
	}, nil
}

// ---- Buckets ----

func (c *Client) ListBuckets(ctx context.Context) ([]Bucket, error) {
	out, err := c.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	buckets := make([]Bucket, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		created := ""
		if b.CreationDate != nil {
			created = b.CreationDate.Format(time.RFC3339)
		}
		buckets = append(buckets, Bucket{Name: aws.ToString(b.Name), Created: created})
	}
	return buckets, nil
}

func (c *Client) CreateBucket(ctx context.Context, name string) error {
	_, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(name)})
	return err
}

func (c *Client) DeleteBucket(ctx context.Context, name string) error {
	_, err := c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)})
	return err
}

// ---- Objects ----

func (c *Client) ListObjects(ctx context.Context, bucket, prefix string) (*Listing, error) {
	out, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    aws.String(bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String("/"),
	})
	if err != nil {
		return nil, err
	}
	l := &Listing{Prefixes: []string{}, Objects: []Object{}}
	for _, p := range out.CommonPrefixes {
		l.Prefixes = append(l.Prefixes, aws.ToString(p.Prefix))
	}
	for _, o := range out.Contents {
		key := aws.ToString(o.Key)
		if key == prefix {
			continue // the prefix "folder" placeholder itself
		}
		mod := ""
		if o.LastModified != nil {
			mod = o.LastModified.Format(time.RFC3339)
		}
		l.Objects = append(l.Objects, Object{Key: key, Size: aws.ToInt64(o.Size), Modified: mod})
	}
	return l, nil
}

func (c *Client) Upload(ctx context.Context, bucket, key string, body io.Reader, contentType string) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	_, err := c.s3.PutObject(ctx, in)
	return err
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	return err
}

// Head returns metadata for a single object.
func (c *Client) Head(ctx context.Context, bucket, key string) (*Object, string, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", err
	}
	mod := ""
	if out.LastModified != nil {
		mod = out.LastModified.Format(time.RFC3339)
	}
	return &Object{Key: key, Size: aws.ToInt64(out.ContentLength), Modified: mod}, aws.ToString(out.ContentType), nil
}

// Download returns the object body and content type; caller must close the body.
func (c *Client) Download(ctx context.Context, bucket, key string) (io.ReadCloser, string, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", err
	}
	return out.Body, aws.ToString(out.ContentType), nil
}

// ---- Links ----

// ObjectLink returns a permanent public URL when the bucket has public access
// (via the Cloudflare API), otherwise a time-limited presigned URL.
func (c *Client) ObjectLink(ctx context.Context, bucket, key string) (*Link, error) {
	if host := c.publicHost(ctx, bucket); host != "" {
		return &Link{
			URL:  fmt.Sprintf("https://%s/%s", host, escapeKey(key)),
			Type: "public",
		}, nil
	}

	const ttl = 7 * 24 * time.Hour
	req, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return nil, err
	}
	return &Link{URL: req.URL, Type: "presigned", Expires: "expires in 7 days"}, nil
}

// publicHost resolves a bucket's public hostname, caching the result. Returns
// "" if the bucket is private or detection is unavailable.
func (c *Client) publicHost(ctx context.Context, bucket string) string {
	if !c.cfg.HasCFToken() {
		return ""
	}

	c.domainMu.Lock()
	if d, ok := c.domains[bucket]; ok && d.fetched {
		c.domainMu.Unlock()
		return d.host
	}
	c.domainMu.Unlock()

	host := c.fetchPublicHost(ctx, bucket)

	c.domainMu.Lock()
	c.domains[bucket] = bucketDomain{fetched: true, host: host}
	c.domainMu.Unlock()
	return host
}

func (c *Client) fetchPublicHost(ctx context.Context, bucket string) string {
	// Prefer a connected custom domain, fall back to the managed r2.dev domain.
	if h := c.customDomain(ctx, bucket); h != "" {
		log.Printf("link: bucket %q -> custom domain %q", bucket, h)
		return h
	}
	if h := c.managedDomain(ctx, bucket); h != "" {
		log.Printf("link: bucket %q -> r2.dev domain %q (no enabled custom domain found)", bucket, h)
		return h
	}
	log.Printf("link: bucket %q -> private (no public domain), will presign", bucket)
	return ""
}

func (c *Client) managedDomain(ctx context.Context, bucket string) string {
	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			Enabled bool   `json:"enabled"`
			Domain  string `json:"domain"`
		} `json:"result"`
	}
	path := fmt.Sprintf("/accounts/%s/r2/buckets/%s/domains/managed", c.cfg.AccountID, bucket)
	if err := c.cfGet(ctx, path, &resp); err != nil {
		return ""
	}
	if resp.Success && resp.Result.Enabled && resp.Result.Domain != "" {
		return resp.Result.Domain
	}
	return ""
}

func (c *Client) customDomain(ctx context.Context, bucket string) string {
	var resp struct {
		Success bool `json:"success"`
		Result  struct {
			Domains []struct {
				Domain  string `json:"domain"`
				Enabled bool   `json:"enabled"`
				Status  struct {
					Ownership string `json:"ownership"`
					SSL       string `json:"ssl"`
				} `json:"status"`
			} `json:"domains"`
		} `json:"result"`
	}
	path := fmt.Sprintf("/accounts/%s/r2/buckets/%s/domains/custom", c.cfg.AccountID, bucket)
	if err := c.cfGet(ctx, path, &resp); err != nil {
		log.Printf("link: custom-domain lookup for %q failed: %v", bucket, err)
		return ""
	}
	if !resp.Success {
		return ""
	}
	// A domain is usable if it's enabled OR Cloudflare reports ownership active.
	// Different accounts/states report these slightly differently, so be lenient.
	for _, d := range resp.Result.Domains {
		if d.Domain == "" {
			continue
		}
		if d.Enabled || strings.EqualFold(d.Status.Ownership, "active") {
			return d.Domain
		}
	}
	if len(resp.Result.Domains) > 0 {
		log.Printf("link: bucket %q has %d custom domain(s) but none enabled/active yet", bucket, len(resp.Result.Domains))
	}
	return ""
}

func (c *Client) cfGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.CFAPIToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloudflare api %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// escapeKey percent-encodes each path segment while keeping slashes intact.
func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// IsNotFound reports whether an error is an S3 NoSuchKey/NoSuchBucket.
func IsNotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nsb *types.NoSuchBucket
	return errors.As(err, &nsk) || errors.As(err, &nsb)
}
