// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package cron_test

import (
	"fmt"
	"testing"
	"time"

	"zgo.at/goatcounter"
	"zgo.at/goatcounter/gctest"
	"zgo.at/zstd/zjson"
)

func TestHitStats(t *testing.T) {
	ctx, clean := gctest.DB(t)
	defer clean()

	site := goatcounter.MustGetSite(ctx)
	now := time.Date(2019, 8, 31, 14, 42, 0, 0, time.UTC)

	gctest.StoreHits(ctx, t, false, []goatcounter.Hit{
		{Site: site.ID, CreatedAt: now, Path: "/asd", Title: "aSd", FirstVisit: true},
		{Site: site.ID, CreatedAt: now, Path: "/asd/"}, // Trailing / should be sanitized and treated identical as /asd
		{Site: site.ID, CreatedAt: now, Path: "/zxc"},
	}...)

	var stats goatcounter.HitStats
	display, displayUnique, more, err := stats.List(ctx, now.Add(-1*time.Hour), now.Add(1*time.Hour), nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	gotT := fmt.Sprintf("%d %d %t", display, displayUnique, more)
	wantT := "3 1 false"
	if wantT != gotT {
		t.Fatalf("wrong totals\ngot:  %s\nwant: %s", gotT, wantT)
	}
	if len(stats) != 2 {
		t.Fatalf("len(stats) is not 2: %d", len(stats))
	}

	want0 := `{"Count":2,"CountUnique":1,"PathID":1,"Path":"/asd","Event":false,"Title":"aSd","RefScheme":null,"Max":2,"Stats":[{"Day":"2019-08-31","Hourly":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,2,0,0,0,0,0,0,0,0,0],"HourlyUnique":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,0,0,0,0,0,0,0,0,0],"Daily":2,"DailyUnique":1}]}`
	got0 := string(zjson.MustMarshal(stats[0]))
	if got0 != want0 {
		t.Errorf("first wrong\ngot:  %s\nwant: %s", got0, want0)
	}

	want1 := `{"Count":1,"CountUnique":0,"PathID":2,"Path":"/zxc","Event":false,"Title":"","RefScheme":null,"Max":1,"Stats":[{"Day":"2019-08-31","Hourly":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,1,0,0,0,0,0,0,0,0,0],"HourlyUnique":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"Daily":1,"DailyUnique":0}]}`
	got1 := string(zjson.MustMarshal(stats[1]))
	if got1 != want1 {
		t.Errorf("second wrong\ngot:  %s\nwant: %s", got1, want1)
	}
}
