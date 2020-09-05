// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package cron_test

import (
	"fmt"
	"testing"
	"time"

	"zgo.at/goatcounter"
	"zgo.at/goatcounter/cron"
	"zgo.at/goatcounter/gctest"
	"zgo.at/zdb"
)

func TestDataRetention(t *testing.T) {
	ctx, clean := gctest.DB(t)
	defer clean()

	site := goatcounter.Site{Code: "bbbb", Plan: goatcounter.PlanPersonal,
		Settings: goatcounter.SiteSettings{DataRetention: 30}}
	err := site.Insert(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = goatcounter.WithSite(ctx, &site)

	now := time.Now().UTC()
	past := now.Add(-40 * 24 * time.Hour)

	gctest.StoreHits(ctx, t, false, []goatcounter.Hit{
		{Site: site.ID, CreatedAt: now, Path: "/a", FirstVisit: zdb.Bool(true)},
		{Site: site.ID, CreatedAt: now, Path: "/a", FirstVisit: zdb.Bool(false)},
		{Site: site.ID, CreatedAt: past, Path: "/a", FirstVisit: zdb.Bool(true)},
		{Site: site.ID, CreatedAt: past, Path: "/a", FirstVisit: zdb.Bool(false)},
	}...)

	err = cron.DataRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var hits goatcounter.Hits
	err = hits.TestList(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("len(hits) is %d\n%v", len(hits), hits)
	}

	var stats goatcounter.HitStats
	display, displayUnique, more, err := stats.List(ctx, past.Add(-1*24*time.Hour), now, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	out := fmt.Sprintf("%d %d %t %v", display, displayUnique, more, err)
	want := `2 1 false <nil>`
	if out != want {
		t.Errorf("\ngot:  %s\nwant: %s", out, want)
	}
}
