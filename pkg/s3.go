package pkg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3ObjectInfo struct {
	Key       *string
	CommitSHA string
}

// processes response of ListObjectsV2 against aws api
// return is map of destination PID to s3ObjectInfo
// Context: within s3, our uploaded object keys are based64 encoded jsons
func (u *Uploader) getS3Keys(ctx context.Context) (map[string]*s3ObjectInfo, error) {
	ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	res, err := u.s3Client.ListObjectsV2(ctxTimeout, &s3.ListObjectsV2Input{
		Bucket: &u.bucket,
	})
	if err != nil {
		return nil, err
	}

	s3ObjectInfos := make(map[string]*s3ObjectInfo)
	for _, obj := range res.Contents {
		// remove file extension before attempting decode
		// extension is .tar.gpg, split at first occurrence of .
		encodedKey := strings.SplitN(*obj.Key, ".", 2)[0]
		decodedBytes, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return nil, err
		}
		var jsonKey DecodedKey
		err = json.Unmarshal(decodedBytes, &jsonKey)
		if err != nil {
			return nil, err
		}
		pid := fmt.Sprintf("%s/%s", jsonKey.Group, jsonKey.ProjectName)
		s3ObjectInfos[pid] = &s3ObjectInfo{
			Key:       obj.Key,
			CommitSHA: jsonKey.CommitSHA,
		}
	}
	return s3ObjectInfos, nil
}

// concurrently deletes objects from s3 sync bucket that are no longer needed
func (u *Uploader) removeOutdated(ctx context.Context, toDeleteKeys []*string) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()

	var wg *sync.WaitGroup
	ch := make(chan error)

	for _, key := range toDeleteKeys {
		wg.Add(1)
		go func(k *string) {
			defer wg.Done()

			_, err := u.s3Client.DeleteObject(ctxTimeout, &s3.DeleteObjectInput{
				Bucket: &u.bucket,
				Key:    k,
			})
			if err != nil {
				ch <- err
			}
		}(key)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for err := range ch {
		if err != nil {
			return err
		}
	}

	return nil
}
