package blobtesting

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"

	"github.com/kopia/kopia/internal/gather"
	"github.com/kopia/kopia/internal/providervalidation"
	"github.com/kopia/kopia/repo/blob"
)

// VerifyStorage verifies the behavior of the specified storage.
// nolint:gocyclo,thelper
func VerifyStorage(ctx context.Context, t *testing.T, r blob.Storage, opts blob.PutOptions) {
	blocks := []struct {
		blk      blob.ID
		contents []byte
	}{
		{blk: "abcdbbf4f0507d054ed5a80a5b65086f602b", contents: []byte{}},
		{blk: "zxce0e35630770c54668a8cfb4e414c6bf8f", contents: []byte{1}},
		{blk: "abff4585856ebf0748fd989e1dd623a8963d", contents: bytes.Repeat([]byte{1}, 1000)},
		{blk: "abgc3dca496d510f492c858a2df1eb824e62", contents: bytes.Repeat([]byte{1}, 10000)},
		{blk: "kopia.repository", contents: bytes.Repeat([]byte{2}, 100)},
	}

	// First verify that blocks don't exist.
	t.Run("VerifyBlobsNotFound", func(t *testing.T) {
		for _, b := range blocks {
			b := b

			t.Run(string(b.blk), func(t *testing.T) {
				t.Parallel()

				AssertGetBlobNotFound(ctx, t, r, b.blk)
				AssertGetMetadataNotFound(ctx, t, r, b.blk)
			})
		}
	})

	if err := r.DeleteBlob(ctx, "no-such-blob"); err != nil && !errors.Is(err, blob.ErrBlobNotFound) {
		t.Errorf("invalid error when deleting non-existent blob: %v", err)
	}

	initialAddConcurrency := 2
	if os.Getenv("CI") != "" {
		initialAddConcurrency = 4
	}

	// Now add blocks.
	t.Run("AddBlobs", func(t *testing.T) {
		for _, b := range blocks {
			for i := 0; i < initialAddConcurrency; i++ {
				b := b

				t.Run(fmt.Sprintf("%v-%v", b.blk, i), func(t *testing.T) {
					t.Parallel()

					if err := r.PutBlob(ctx, b.blk, gather.FromSlice(b.contents), opts); err != nil {
						t.Fatalf("can't put blob: %v", err)
					}
				})
			}
		}
	})

	t.Run("GetBlobs", func(t *testing.T) {
		for _, b := range blocks {
			b := b

			t.Run(string(b.blk), func(t *testing.T) {
				t.Parallel()

				AssertGetBlob(ctx, t, r, b.blk, b.contents)
			})
		}
	})

	t.Run("ListBlobs", func(t *testing.T) {
		errExpected := errors.New("expected error")

		t.Run("ListErrorNoPrefix", func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, r.ListBlobs(ctx, "", func(bm blob.Metadata) error {
				return errExpected
			}), errExpected)
		})
		t.Run("ListErrorWithPrefix", func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, r.ListBlobs(ctx, "ab", func(bm blob.Metadata) error {
				return errExpected
			}), errExpected)
		})
		t.Run("ListNoPrefix", func(t *testing.T) {
			t.Parallel()
			AssertListResults(ctx, t, r, "", blocks[0].blk, blocks[1].blk, blocks[2].blk, blocks[3].blk, blocks[4].blk)
		})
		t.Run("ListWithPrefix", func(t *testing.T) {
			t.Parallel()
			AssertListResults(ctx, t, r, "ab", blocks[0].blk, blocks[2].blk, blocks[3].blk)
		})
	})

	t.Run("OverwriteBlobs", func(t *testing.T) {
		for _, b := range blocks {
			b := b

			t.Run(string(b.blk), func(t *testing.T) {
				t.Parallel()

				require.NoErrorf(t, r.PutBlob(ctx, b.blk, gather.FromSlice(b.contents), blob.PutOptions{}), "can't put blob: %v", b)
				AssertGetBlob(ctx, t, r, b.blk, b.contents)
			})
		}
	})

	ts := time.Date(2020, 1, 1, 15, 30, 45, 0, time.UTC)

	t.Run("SetTime", func(t *testing.T) {
		for _, b := range blocks {
			b := b

			t.Run(string(b.blk), func(t *testing.T) {
				t.Parallel()

				err := r.SetTime(ctx, b.blk, ts)
				if errors.Is(err, blob.ErrSetTimeUnsupported) {
					return
				}

				require.NoError(t, err)

				md, err := r.GetMetadata(ctx, b.blk)
				if err != nil {
					t.Errorf("unable to get blob metadata")
				}

				require.True(t, md.Timestamp.Equal(ts), "invalid time after SetTme(): %vm want %v", md.Timestamp, ts)
			})
		}
	})

	require.NoError(t, r.DeleteBlob(ctx, blocks[0].blk))
	require.NoError(t, r.DeleteBlob(ctx, blocks[0].blk))

	AssertListResults(ctx, t, r, "ab", blocks[2].blk, blocks[3].blk)
	AssertListResults(ctx, t, r, "", blocks[1].blk, blocks[2].blk, blocks[3].blk, blocks[4].blk)
}

// AssertConnectionInfoRoundTrips verifies that the ConnectionInfo returned by a given storage can be used to create
// equivalent storage.
// nolint:thelper
func AssertConnectionInfoRoundTrips(ctx context.Context, t *testing.T, s blob.Storage) {
	ci := s.ConnectionInfo()

	s2, err := blob.NewStorage(ctx, ci, false)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	ci2 := s2.ConnectionInfo()
	require.Equal(t, ci, ci2)

	require.NoError(t, s2.Close(ctx))
}

// TestValidationOptions is the set of options used when running providing validation from tests.
// nolint:gomnd
var TestValidationOptions = providervalidation.Options{
	MaxClockDrift:           3 * time.Minute,
	ConcurrencyTestDuration: 15 * time.Second,
	NumPutBlobWorkers:       3,
	NumGetBlobWorkers:       3,
	NumGetMetadataWorkers:   3,
	NumListBlobsWorkers:     3,
	MaxBlobLength:           10e6,
}
