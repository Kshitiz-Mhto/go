package index

import (
	"bytes"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/stellar/go/support/log"
)

const BUCKET = "horizon-index"

type S3Backend struct {
	s3Session  *session.Session
	downloader *s3manager.Downloader
	uploader   *s3manager.Uploader
	parallel   uint32
}

func NewS3Store(awsConfig *aws.Config, parallel uint32) (Store, error) {
	backend, err := NewS3Backend(awsConfig, parallel)
	if err != nil {
		return nil, err
	}
	return NewStore(backend)
}

func NewS3Backend(awsConfig *aws.Config, parallel uint32) (*S3Backend, error) {
	s3Session, err := session.NewSession(awsConfig)
	if err != nil {
		return nil, err
	}

	return &S3Backend{
		s3Session:  s3Session,
		downloader: s3manager.NewDownloader(s3Session),
		uploader:   s3manager.NewUploader(s3Session),
		parallel:   parallel,
	}, nil
}

func (s *S3Backend) Flush(indexes map[string]map[string]*CheckpointIndex) error {
	return parallelFlush(s.parallel, indexes, s.writeBatch)
}

func (s *S3Backend) writeBatch(b *batch, r retry) error {
	var buf bytes.Buffer
	if _, err := writeGzippedTo(&buf, b.indexes); err != nil {
		// TODO: Should we retry or what here??
		return fmt.Errorf("unable to serialize %s: %v", b.account, err)
	}

	_, err := s.uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(BUCKET),
		Key:    aws.String(b.account),
		Body:   &buf,
	})
	if err != nil {
		r(b)
		return fmt.Errorf("unable to upload %s, %v", b.account, err)
	}

	return nil
}

func (s *S3Backend) Read(account string) (map[string]*CheckpointIndex, error) {
	// Check if index exists in S3
	log.Debugf("Downloading index: %s", account)
	b := &aws.WriteAtBuffer{}
	_, err := s.downloader.Download(b, &s3.GetObjectInput{
		Bucket: aws.String(BUCKET),
		Key:    aws.String(account),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchKey {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	indexes, _, err := readGzippedFrom(bytes.NewReader(b.Bytes()))
	return indexes, err
}