// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package goatcounter

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"zgo.at/errors"
	"zgo.at/goatcounter/cfg"
	"zgo.at/zdb"
	"zgo.at/zstd/zint"
	"zgo.at/zstd/zjson"
)

var allDays = []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

// List the top paths for this site in the given time period.
func (h *HitStats) List(
	ctx context.Context, start, end time.Time, filter string, exclude []int64, daily bool,
) (int, int, bool, error) {
	db := zdb.MustGet(ctx)
	site := MustGetSite(ctx)

	if filter != "" {
		filter = "%" + strings.ToLower(filter) + "%"
	}

	// List the pages for this page.
	var more bool
	{
		// Get one page more so we can detect if there are more pages after this.
		limit := int(zint.NonZero(int64(site.Settings.Limits.Page), 10)) + 1

		// ee := make([]int64, len(exclude))
		// for i := range exclude {
		// 	n, _ := strconv.ParseInt(exclude[i], 10, 64)
		// 	ee[i] = n
		// }

		query, args, err := zdb.Query(db, `/* HitStats.List */
			with x as (
				select path_id from hit_counts
				{{join paths using (path_id)}}
				where
					hit_counts.site_id=:site and
					{{path_id not in (:exclude) and}}
					hour>=:start and
					hour<=:end
					{{and (lower(path) like :filter or lower(title) like :filter)}}
				group by path_id
				order by sum(total_unique) desc, path_id desc
				limit :limit
			)
			select path_id, paths.path, paths.title, paths.event from x
			join paths using (path_id)
		`, struct {
			Site               int64
			Start, End, Filter string
			Limit              int
			Exclude            []int64
		}{site.ID, start.Format(zdb.Date), end.Format(zdb.Date), filter, limit, exclude},
			filter != "", len(exclude) > 0, filter != "")
		if err != nil {
			return 0, 0, false, errors.Wrap(err, "HitStats.List")
		}

		err = db.SelectContext(ctx, h, query, args...)
		if err != nil {
			return 0, 0, false, errors.Wrap(err, "HitStats.List get hit_counts")
		}

		// Check if there are more entries.
		if len(*h) == limit {
			hh := *h
			hh = hh[:len(hh)-1]
			*h = hh
			more = true
		}
	}

	// Add stats
	var st []struct {
		PathID      int64     `db:"path_id"`
		Day         time.Time `db:"day"`
		Stats       []byte    `db:"stats"`
		StatsUnique []byte    `db:"stats_unique"`
	}
	{
		query, args, err := zdb.Query(db, `/* HitStats.List */
			select path_id, day, stats, stats_unique
			from hit_stats
			join paths using (path_id)
			where
				hit_stats.site_id=:site and
				day>=:start and
				day<=:end
				{{and (lower(path) like :filter or lower(title) like :filter)}}
			order by day asc`,
			struct {
				Site               int64
				Start, End, Filter string
			}{site.ID, start.Format("2006-01-02"), end.Format("2006-01-02"), filter},
			filter != "")
		if err != nil {
			return 0, 0, false, errors.Wrap(err, "HitStats.List")
		}

		err = db.SelectContext(ctx, &st, query, args...)
		if err != nil {
			return 0, 0, false, errors.Wrap(err, "HitStats.List get hit_stats")
		}
	}

	hh := *h

	// Add the hit_stats.
	{
		for i := range hh {
			for _, s := range st {
				if s.PathID == hh[i].PathID {
					var x, y []int
					zjson.MustUnmarshal(s.Stats, &x)
					zjson.MustUnmarshal(s.StatsUnique, &y)
					hh[i].Stats = append(hh[i].Stats, Stat{
						Day:          s.Day.Format("2006-01-02"),
						Hourly:       x,
						HourlyUnique: y,
					})
				}
			}
		}
	}

	// Fill in blank days.
	fillBlankDays(hh, start, end)

	// Apply TZ offset.
	applyOffset(hh, *site)

	// Add total and max.
	var totalDisplay, totalUniqueDisplay int
	addTotals(hh, daily, &totalDisplay, &totalUniqueDisplay)

	return totalDisplay, totalUniqueDisplay, more, nil
}

// PathTotals is a special path to indicate this is the "total" overview.
//
// Trailing whitespace is trimmed on paths, so this should never conflict.
const PathTotals = "TOTAL "

// Totals gets the totals overview of all pages.
func (h *HitStat) Totals(ctx context.Context, start, end time.Time, filter string, daily bool) (int, error) {
	db := zdb.MustGet(ctx)
	site := MustGetSite(ctx)

	join := ""
	tbl := "hit_counts"
	if filter != "" {
		join = ` join paths using (path_id) `
		tbl = "paths"
	}

	// select hour, total, total_unique from hit_counts
	// where site_id=$1 and hour>=$2 and hour<=$3 `
	query := `/* HitStat.Totals */
		select hour, total, total_unique
		from hit_counts
		` + join + `
		where ` + tbl + `.site_id=$1 and hour>=$2 and hour<=$3 `
	args := []interface{}{site.ID, start.Format(zdb.Date), end.Format(zdb.Date)}
	if filter != "" {
		query += ` and (lower(path) like lower($4) or lower(title) like lower($4)) `
		args = append(args, "%"+filter+"%")
	}
	query += ` order by hour asc`
	var tc []struct {
		Hour        time.Time `db:"hour"`
		Total       int       `db:"total"`
		TotalUnique int       `db:"total_unique"`
	}
	err := db.SelectContext(ctx, &tc, query, args...)
	if err != nil {
		return 0, errors.Errorf("HitStat.Totals: %w", err)
	}

	totalst := HitStat{
		Path:  PathTotals,
		Title: "",
	}
	stats := make(map[string]Stat)
	for _, t := range tc {
		d := t.Hour.Format("2006-01-02")
		hour, _ := strconv.ParseInt(t.Hour.Format("15"), 10, 32)
		s, ok := stats[d]
		if !ok {
			s = Stat{
				Day:          d,
				Hourly:       make([]int, 24),
				HourlyUnique: make([]int, 24),
			}
		}

		s.Hourly[hour] += t.Total
		s.HourlyUnique[hour] += t.TotalUnique
		totalst.Count += t.Total
		totalst.CountUnique += t.TotalUnique

		stats[d] = s
	}

	max := 0
	for _, v := range stats {
		totalst.Stats = append(totalst.Stats, v)
		if !daily {
			for _, x := range v.Hourly {
				if x > max {
					max = x
				}
			}
		}
	}

	sort.Slice(totalst.Stats, func(i, j int) bool {
		return totalst.Stats[i].Day < totalst.Stats[j].Day
	})

	hh := []HitStat{totalst}
	fillBlankDays(hh, start, end)
	applyOffset(hh, *site)

	if daily {
		for i := range hh[0].Stats {
			for _, n := range hh[0].Stats[i].Hourly {
				hh[0].Stats[i].Daily += n
			}
			for _, n := range hh[0].Stats[i].HourlyUnique {
				hh[0].Stats[i].DailyUnique += n
			}
			if daily && hh[0].Stats[i].Daily > max {
				max = hh[0].Stats[i].Daily
			}
		}
	}

	if max < 10 {
		max = 10
	}

	*h = hh[0]
	return max, nil
}

// The database stores everything in UTC, so we need to apply
// the offset for HitStats.List()
//
// Let's say we have two days with an offset of UTC+2, this means we
// need to transform this:
//
//    2019-12-05 → [0,0,0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0,1,0]
//    2019-12-06 → [0,0,0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0,1,0]
//    2019-12-07 → [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]
//
// To:
//
//    2019-12-05 → [0,0,0,0,0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0]
//    2019-12-06 → [1,0,0,0,0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0]
//    2019-12-07 → [1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]
//
// And skip the first 2 hours of the first day.
//
// Or, for UTC-2:
//
//    2019-12-04 → [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0]
//    2019-12-05 → [0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0,1,0,0,0]
//    2019-12-06 → [0,0,0,0,0,0,0,0,0,4,7,0,0,0,0,0,0,0,0,0,1,0,0,0]
//
// And skip the last 2 hours of the last day.
//
// Offsets that are not whole hours (e.g. 6:30) are treated like 7:00. I don't
// know how to do that otherwise.
func applyOffset(hh HitStats, site Site) {
	if len(hh) == 0 {
		return
	}

	offset := site.Settings.Timezone.Offset()
	if offset%60 != 0 {
		offset += 30
	}
	offset /= 60

	switch {
	case offset > 0:
		for i := range hh {
			stats := hh[i].Stats

			popped := make([]int, offset)
			poppedUnique := make([]int, offset)
			for i := range stats {
				stats[i].Hourly = append(popped, stats[i].Hourly...)
				o := len(stats[i].Hourly) - offset
				popped = stats[i].Hourly[o:]
				stats[i].Hourly = stats[i].Hourly[:o]

				stats[i].HourlyUnique = append(poppedUnique, stats[i].HourlyUnique...)
				poppedUnique = stats[i].HourlyUnique[o:]
				stats[i].HourlyUnique = stats[i].HourlyUnique[:o]
			}
			hh[i].Stats = stats[1:] // Overselect a day to get the stats for it, remove it.
		}

	case offset < 0:
		offset = -offset

		for i := range hh {
			stats := hh[i].Stats

			popped := make([]int, offset)
			poppedUnique := make([]int, offset)
			for i := len(stats) - 1; i >= 0; i-- {
				stats[i].Hourly = append(stats[i].Hourly, popped...)
				popped = stats[i].Hourly[:offset]
				stats[i].Hourly = stats[i].Hourly[offset:]

				stats[i].HourlyUnique = append(stats[i].HourlyUnique, poppedUnique...)
				poppedUnique = stats[i].HourlyUnique[:offset]
				stats[i].HourlyUnique = stats[i].HourlyUnique[offset:]
			}
			hh[i].Stats = stats[:len(stats)-1] // Overselect a day to get the stats for it, remove it.
		}
	}
}

func fillBlankDays(hh HitStats, start, end time.Time) {
	// Should Never Happen™ but if it does the below loop will never break, so
	// be safe.
	if start.After(end) {
		return
	}

	endFmt := end.Format("2006-01-02")
	for i := range hh {
		var (
			day     = start.Add(-24 * time.Hour)
			newStat []Stat
			j       int
		)

		for {
			day = day.Add(24 * time.Hour)
			dayFmt := day.Format("2006-01-02")

			if len(hh[i].Stats)-1 >= j && dayFmt == hh[i].Stats[j].Day {
				newStat = append(newStat, hh[i].Stats[j])
				j++
			} else {
				newStat = append(newStat, Stat{Day: dayFmt, Hourly: allDays, HourlyUnique: allDays})
			}
			if dayFmt == endFmt {
				break
			}
		}

		hh[i].Stats = newStat
	}
}

func addTotals(hh HitStats, daily bool, totalDisplay, totalUniqueDisplay *int) {
	for i := range hh {
		for j := range hh[i].Stats {
			for k := range hh[i].Stats[j].Hourly {
				hh[i].Stats[j].Daily += hh[i].Stats[j].Hourly[k]
				hh[i].Stats[j].DailyUnique += hh[i].Stats[j].HourlyUnique[k]
				if !daily && hh[i].Stats[j].Hourly[k] > hh[i].Max {
					hh[i].Max = hh[i].Stats[j].Hourly[k]
				}
			}

			hh[i].Count += hh[i].Stats[j].Daily
			hh[i].CountUnique += hh[i].Stats[j].DailyUnique
			if daily && hh[i].Stats[j].Daily > hh[i].Max {
				hh[i].Max = hh[i].Stats[j].Daily
			}
		}

		*totalDisplay += hh[i].Count
		*totalUniqueDisplay += hh[i].CountUnique
	}

	// We sort in SQL, but this is not always 100% correct after applying
	// the TZ offset, so order here as well.
	//
	// TODO: this is still not 100% correct, as the "first 10" after
	// applying the TZ offset may be different than the first 10 being
	// fetched in the SQL query. There is no easy fix for that in the
	// current design. I considered storing everything in the DB as the
	// configured TZ, but that would make changing the TZ expensive, I'm not
	// 100% sure yet what a good solution here is. For now, this is "good
	// enough".
	sort.Slice(hh, func(i, j int) bool { return hh[i].CountUnique > hh[j].CountUnique })
}

func GetTotalCount(ctx context.Context, start, end time.Time, filter string) (int, int, error) {
	join := ""
	tbl := "hit_counts"
	if filter != "" {
		join = ` join paths using (path_id) `
		tbl = "paths"
	}

	query := `/* GetTotalCount */
		select
			coalesce(sum(total), 0) as t,
			coalesce(sum(total_unique), 0) as u
			from hit_counts
			` + join + `
			where
				` + tbl + `.site_id=$1 and
				hour>=$2 and
				hour<=$3 `
	args := []interface{}{MustGetSite(ctx).ID, start.Format(zdb.Date), end.Format(zdb.Date)}
	if filter != "" {
		query += ` and (lower(path) like $4 or lower(title) like $4) `
		args = append(args, "%"+strings.ToLower(filter)+"%")
	}

	var t struct{ T, U int }
	err := zdb.MustGet(ctx).GetContext(ctx, &t, query, args...)
	return t.T, t.U, errors.Wrap(err, "GetTotalCount")
}

func GetTotalCountUTC(ctx context.Context, start, end time.Time, filter string) (int, int, error) {
	start = start.In(MustGetSite(ctx).Settings.Timezone.Location)
	end = end.In(MustGetSite(ctx).Settings.Timezone.Location)

	query := `/* GetTotalCountUTC */
		select
			coalesce(sum(total), 0) as t,
			coalesce(sum(total_unique), 0) as u
		from hit_counts where
			site_id=$1 and
			hour>=$2 and
			hour<=$3 `

	args := []interface{}{MustGetSite(ctx).ID, start.Format(zdb.Date), end.Format(zdb.Date)}
	if filter != "" {
		query += ` and (lower(path) like $4 or lower(title) like $4) `
		args = append(args, "%"+strings.ToLower(filter)+"%")
	}

	var t struct{ T, U int }
	err := zdb.MustGet(ctx).GetContext(ctx, &t, query, args...)
	return t.T, t.U, errors.Wrap(err, "GetTotalCount")
}

func GetMax(ctx context.Context, start, end time.Time, filter string, daily bool) (int, error) {
	if filter != "" {
		filter = "%" + filter + "%"
	}

	site := MustGetSite(ctx)
	var (
		max   int
		query string
		args  []interface{}
		err   error
	)
	if daily {
		query, args, err = zdb.Query(zdb.MustGet(ctx), `/* getMax daily */
			with x as (
				select path_id from paths
				where
					site_id=:site
					{{and (lower(path) like :filter or lower(title) like :filter)}}
			)
			select coalesce(sum(total), 0) as t
			from hit_counts, x
			where
				hit_counts.path_id=x.path_id and
				hit_counts.site_id=1 and
				hour>=:start and hour<=:end
			{{group by hit_counts.path_id, date(hour, :tz)}}
			{{group by hit_counts.path_id, date(timezone(:tz, hour))}}
			order by t desc
			limit 1 `,
			struct {
				Site                   int64
				Start, End, TZ, Filter string
			}{site.ID, start.Format(zdb.Date), end.Format(zdb.Date),
				site.Settings.Timezone.OffsetRFC3339(), filter},
			filter != "", !cfg.PgSQL, cfg.PgSQL)
	} else {
		query, args, err = zdb.Query(zdb.MustGet(ctx), `/* getMax hourly */
				select coalesce(max(total), 0) from hit_counts
				{{join paths using(path_id)}}
				where hit_counts.site_id=:site and hour>=:start and hour<=:end
				{{and (lower(path) like :filter or lower(title) like :filter)}}`,
			struct {
				Site               int64
				Start, End, Filter string
			}{site.ID, start.Format(zdb.Date), end.Format(zdb.Date), filter},
			filter != "", filter != "")
	}
	if err != nil {
		return 0, errors.Wrap(err, "getMax")
	}

	db := zdb.MustGet(ctx)
	err = db.GetContext(ctx, &max, query, args...)
	if err != nil && !zdb.ErrNoRows(err) {
		return 0, errors.Wrap(err, "getMax")
	}

	if max < 10 {
		max = 10
	}
	return max, nil
}
