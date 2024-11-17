package lmdb

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/PowerDNS/lmdb-go/lmdb"
	"github.com/fiatjaf/eventstore"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func FuzzQuery(f *testing.F) {
	ctx := context.Background()

	f.Add(uint(200), uint(50), uint(13), uint(2), uint(2), uint(0), uint(1))
	f.Fuzz(func(t *testing.T, total, limit, authors, timestampAuthorFactor, seedFactor, kinds, kindFactor uint) {
		total++
		authors++
		seedFactor++
		kindFactor++
		if kinds == 1 {
			kinds++
		}
		if limit == 0 {
			return
		}

		// ~ setup db
		db := &LMDBBackend{}
		db.Path = "/tmp/lmdbtest"
		db.extraFlags = lmdb.NoSync
		db.MaxLimit = 500
		if err := db.Init(); err != nil {
			t.Fatal(err)
			return
		}
		defer db.Close()

		// ~ start actual test

		filter := nostr.Filter{
			Authors: make([]string, authors),
			Limit:   int(limit),
		}
		maxKind := 1
		if kinds > 0 {
			filter.Kinds = make([]int, kinds)
			for i := range filter.Kinds {
				filter.Kinds[i] = int(kindFactor) * i
			}
			maxKind = filter.Kinds[len(filter.Kinds)-1]
		}

		for i := 0; i < int(authors); i++ {
			sk := make([]byte, 32)
			binary.BigEndian.PutUint32(sk, uint32(i%int(authors*seedFactor))+1)
			pk, _ := nostr.GetPublicKey(hex.EncodeToString(sk))
			filter.Authors[i] = pk
		}

		expected := make([]*nostr.Event, 0, total)
		for i := 0; i < int(total); i++ {
			skseed := uint32(i%int(authors*seedFactor)) + 1
			sk := make([]byte, 32)
			binary.BigEndian.PutUint32(sk, skseed)

			evt := &nostr.Event{
				CreatedAt: nostr.Timestamp(skseed)*nostr.Timestamp(timestampAuthorFactor) + nostr.Timestamp(i),
				Content:   fmt.Sprintf("unbalanced %d", i),
				Tags:      nostr.Tags{},
				Kind:      i % maxKind,
			}
			err := evt.Sign(hex.EncodeToString(sk))
			require.NoError(t, err)

			err = db.SaveEvent(ctx, evt)
			require.NoError(t, err)

			if filter.Matches(evt) {
				expected = append(expected, evt)
			}
		}

		slices.SortFunc(expected, nostr.CompareEventPtrReverse)
		if len(expected) > int(limit) {
			expected = expected[0:limit]
		}

		w := eventstore.RelayWrapper{Store: db}

		start := time.Now()
		// fmt.Println(filter)
		res, err := w.QuerySync(ctx, filter)
		end := time.Now()

		require.NoError(t, err)
		require.Equal(t, len(expected), len(res), "number of results is different than expected")

		require.Less(t, end.Sub(start).Milliseconds(), int64(1500), "query took too long")
		require.True(t, slices.IsSortedFunc(res, func(a, b *nostr.Event) int { return cmp.Compare(b.CreatedAt, a.CreatedAt) }), "results are not sorted")

		nresults := len(expected)

		getTimestamps := func(events []*nostr.Event) []nostr.Timestamp {
			res := make([]nostr.Timestamp, len(events))
			for i, evt := range events {
				res[i] = evt.CreatedAt
			}
			return res
		}

		// fmt.Println(" expected   result")
		// ets := getTimestamps(expected)
		// rts := getTimestamps(res)
		// for i := range ets {
		// 	fmt.Println(" ", ets[i], "  ", rts[i], "           ", i)
		// }

		require.Equal(t, expected[0].CreatedAt, res[0].CreatedAt, "first result is wrong")
		require.Equal(t, expected[nresults-1].CreatedAt, res[nresults-1].CreatedAt, "last result is wrong")
		require.Equal(t, getTimestamps(expected), getTimestamps(res))

		for _, evt := range res {
			require.True(t, filter.Matches(evt), "event %s doesn't match filter %s", evt, filter)
		}
	})
}
