package objectstore

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ListKeys includes historical versions and delete markers; a versioned object is still retained data.
func (s *S3Store) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	if prefix == "" {
		return nil, errors.New("cleanup requires a bounded prefix")
	}
	seen := map[string]bool{}
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			key := aws.ToString(o.Key)
			if !strings.HasPrefix(key, prefix) {
				return nil, errors.New("object scope mismatch")
			}
			seen[key] = true
		}
	}
	versions := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for versions.HasMorePages() {
		page, err := versions.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Versions {
			key := aws.ToString(v.Key)
			if !strings.HasPrefix(key, prefix) {
				return nil, errors.New("object scope mismatch")
			}
			seen[key] = true
		}
		for _, v := range page.DeleteMarkers {
			key := aws.ToString(v.Key)
			if !strings.HasPrefix(key, prefix) {
				return nil, errors.New("object scope mismatch")
			}
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// PurgeKey deletes every version of exactly one key, then verifies absence.
func (s *S3Store) PurgeKey(ctx context.Context, key string) error {
	if key == "" {
		return errors.New("empty cleanup key")
	}
	// Enumerate before mutating so pagination cannot skip versions as the listing shrinks.
	ids := []*string{}
	pages := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{Bucket: aws.String(s.bucket), Prefix: aws.String(key)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, v := range page.Versions {
			if aws.ToString(v.Key) == key {
				ids = append(ids, v.VersionId)
			}
		}
		for _, v := range page.DeleteMarkers {
			if aws.ToString(v.Key) == key {
				ids = append(ids, v.VersionId)
			}
		}
	}
	if len(ids) == 0 {
		ids = append(ids, aws.String("null"))
	}
	for _, id := range ids {
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), VersionId: id}); err != nil {
			return err
		}
	}
	keys, err := s.ListKeys(ctx, key)
	if err != nil {
		return err
	}
	for _, left := range keys {
		if left == key {
			return errors.New("object versions remain after deletion")
		}
	}
	if _, err = s.Head(ctx, key); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("object remains after deletion")
	}
	return nil
}
