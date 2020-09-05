// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package goatcounter_test

import (
	"context"
	"testing"

	. "zgo.at/goatcounter"
	"zgo.at/goatcounter/gctest"
	"zgo.at/zdb"
)

func TestMemstore(t *testing.T) {
	ctx, clean := gctest.DB(t)
	defer clean()

	for i := 0; i < 2000; i++ {
		Memstore.Append(gen(ctx))
	}

	_, err := Memstore.Persist(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var count int
	err = zdb.MustGet(ctx).GetContext(ctx, &count, `select count(*) from hits`)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2000 {
		t.Errorf("wrong count; wanted 2000 but got %d", count)
	}
}

func gen(ctx context.Context) Hit {
	s := MustGetSite(ctx)
	return Hit{
		Site:            s.ID,
		Session:         TestSession,
		Path:            "/test",
		Ref:             "https://example.com/test",
		UserAgentHeader: "test",
	}
}

func TestNextUUID(t *testing.T) {
	want := `11223344556677-8899aabbccddef01
11223344556677-8899aabbccddef02
11223344556677-8899aabbccddef03
11223344556677-8899aabbccddeeff`

	func() {
		_, clean := gctest.DB(t)
		defer clean()

		got := Memstore.SessionID().Format(16) + "\n" +
			Memstore.SessionID().Format(16) + "\n" +
			Memstore.SessionID().Format(16) + "\n" +
			TestSession.Format(16)
		if got != want {
			t.Errorf("wrong:\n%s", got)
		}
	}()

	func() {
		_, clean := gctest.DB(t)
		defer clean()

		got := Memstore.SessionID().Format(16) + "\n" +
			Memstore.SessionID().Format(16) + "\n" +
			Memstore.SessionID().Format(16) + "\n" +
			TestSession.Format(16)
		if got != want {
			t.Errorf("wrong after reset:\n%s", got)
		}
	}()
}
