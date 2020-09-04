// Copyright © 2019 Martin Tournoij – This file is part of GoatCounter and
// published under the terms of a slightly modified EUPL v1.2 license, which can
// be found in the LICENSE file or at https://license.goatcounter.com

package goatcounter

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"zgo.at/blackmail"
	"zgo.at/errors"
	"zgo.at/gadget"
	"zgo.at/goatcounter/cfg"
	"zgo.at/zdb"
	"zgo.at/zlog"
	"zgo.at/zstd/zcrypto"
	"zgo.at/zstd/zint"
	"zgo.at/zvalidate"
)

const ExportVersion = "2"

type Export struct {
	ID     int64 `db:"export_id" json:"id,readonly"`
	SiteID int64 `db:"site_id" json:"site_id,readonly"`

	// The hit ID this export was started from.
	StartFromHitID int64 `db:"start_from_hit_id" json:"start_from_hit_id"`

	// Last hit ID that was exported; can be used as start_from_hit_id.
	LastHitID *int64 `db:"last_hit_id" json:"last_hit_id,readonly"`

	Path      string    `db:"path" json:"path,readonly"` // {omitdoc}
	CreatedAt time.Time `db:"created_at" json:"created_at,readonly"`

	FinishedAt *time.Time `db:"finished_at" json:"finished_at,readonly"`
	NumRows    *int       `db:"num_rows" json:"num_rows,readonly"`

	// File size in MB.
	Size *string `db:"size" json:"size,readonly"`

	// SHA256 hash.
	Hash *string `db:"hash" json:"hash,readonly"`

	// Any errors that may have occured.
	Error *string `db:"error" json:"error,readonly"`
}

func (e *Export) ByID(ctx context.Context, id int64) error {
	return errors.Wrapf(zdb.MustGet(ctx).GetContext(ctx, e,
		`/* Export.ByID */ select * from exports where export_id=$1 and site_id=$2`,
		id, MustGetSite(ctx).ID), "Export.ByID %d", id)
}

// Create a new export.
//
// Inserts a row in exports table and returns open file pointer to the
// destination file.
func (e *Export) Create(ctx context.Context, startFrom int64) (*os.File, error) {
	site := MustGetSite(ctx)

	e.SiteID = site.ID
	e.CreatedAt = Now()
	e.StartFromHitID = startFrom
	e.Path = fmt.Sprintf("%s%sgoatcounter-export-%s-%s-%d.csv.gz",
		os.TempDir(), string(os.PathSeparator), site.Code,
		e.CreatedAt.Format("20060102T150405Z"), startFrom)

	var err error
	e.ID, err = insertWithID(ctx, "export_id",
		`insert into exports (site_id, path, created_at, start_from_hit_id) values ($1, $2, $3, $4)`,
		e.SiteID, e.Path, e.CreatedAt.Format(zdb.Date), e.StartFromHitID)
	if err != nil {
		return nil, errors.Wrap(err, "Export.Create")
	}

	fp, err := os.Create(e.Path)
	return fp, errors.Wrap(err, "Export.Create")
}

// Export all data to a CSV file.
func (e *Export) Run(ctx context.Context, fp *os.File, mailUser bool) {
	l := zlog.Module("export").Field("id", e.ID)
	l.Print("export started")

	gzfp := gzip.NewWriter(fp)
	defer fp.Close() // No need to error-check; just for safety.
	defer gzfp.Close()

	c := csv.NewWriter(gzfp)
	c.Write([]string{ExportVersion + "Path", "Title", "Event", "UserAgent",
		"Browser", "System", "Session", "Bot", "Referrer", "Referrer scheme",
		"Screen size", "Location", "FirstVisit", "Date"})

	var exportErr error
	e.LastHitID = &e.StartFromHitID
	var z int
	e.NumRows = &z
	for {
		var (
			hits ExportRows
			last int64
		)
		last, exportErr = hits.Export(ctx, 5000, *e.LastHitID)
		e.LastHitID = &last
		if len(hits) == 0 {
			break
		}
		if exportErr != nil {
			break
		}

		*e.NumRows += len(hits)

		for _, hit := range hits {
			c.Write([]string{hit.Path, hit.Title, hit.Event, hit.UserAgent,
				hit.Browser, hit.System, hit.Session.String(), hit.Bot, hit.Ref,
				unref(hit.RefScheme), hit.Size, hit.Location, hit.FirstVisit,
				hit.CreatedAt})
		}

		c.Flush()
		exportErr = c.Error()
		if exportErr != nil {
			break
		}

		// Small amount of breathing space.
		if cfg.Prod {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if exportErr != nil {
		l.Field("export", e).Error(exportErr)

		_, err := zdb.MustGet(ctx).ExecContext(ctx,
			`update exports set error=$1 where export_id=$2`,
			exportErr.Error(), e.ID)
		if err != nil {
			zlog.Error(err)
		}

		_ = gzfp.Close()
		_ = fp.Close()
		_ = os.Remove(fp.Name())
		return
	}

	err := gzfp.Close()
	if err != nil {
		l.Error(err)
		return
	}
	err = fp.Sync() // Ensure stat is correct.
	if err != nil {
		l.Error(err)
		return
	}

	stat, err := fp.Stat()
	size := "0"
	if err == nil {
		size = fmt.Sprintf("%.1f", float64(stat.Size())/1024/1024)
	}
	e.Size = &size

	err = fp.Close()
	if err != nil {
		l.Error(err)
		return
	}

	hash, err := zcrypto.HashFile(e.Path)
	e.Hash = &hash
	if err != nil {
		l.Error(err)
		return
	}

	now := Now().Format(zdb.Date)
	_, err = zdb.MustGet(ctx).ExecContext(ctx, `update exports set
		finished_at=$1, num_rows=$2, size=$3, hash=$4, last_hit_id=$5
		where export_id=$6`,
		&now, e.NumRows, e.Size, e.Hash, e.LastHitID, e.ID)
	if err != nil {
		zlog.Error(err)
	}

	if mailUser {
		site := MustGetSite(ctx)
		user := GetUser(ctx)
		err = blackmail.Send("GoatCounter export ready",
			blackmail.From("GoatCounter export", cfg.EmailFrom),
			blackmail.To(user.Email),
			blackmail.BodyMustText(EmailTemplate("email_export_done.gotxt", struct {
				Site   Site
				Export Export
			}{*site, *e})))
		if err != nil {
			l.Error(err)
		}
	}
}

type Exports []Export

func (e *Exports) List(ctx context.Context) error {
	return errors.Wrap(zdb.MustGet(ctx).SelectContext(ctx, e, `/* Exports.List */
		select * from exports where site_id=$1 and created_at > `+interval(1),
		MustGetSite(ctx).ID), "Exports.List")
}

// Import data from an export.
func Import(ctx context.Context, fp io.Reader, replace, email bool) {
	site := MustGetSite(ctx)
	user := GetUser(ctx)

	l := zlog.Module("import").Field("site", site.ID).Field("replace", replace)
	l.Print("import started")

	c := csv.NewReader(fp)
	header, err := c.Read()
	if err != nil {
		importError(l, *user, err)
		return
	}

	if len(header) == 0 || !strings.HasPrefix(header[0], ExportVersion) {
		importError(l, *user, errors.Errorf(
			"wrong version of CSV database: %s (expected: %s)",
			header[0][:1], ExportVersion))
		return
	}

	if replace {
		err := site.DeleteAll(ctx)
		if err != nil {
			importError(l, *user, err)
			l.Error(err)
			return
		}
	}

	var (
		sessions = make(map[zint.Uint128]zint.Uint128)
		n        = 0
		errs     = errors.NewGroup(50)
	)
	for {
		line, err := c.Read()
		if err == io.EOF {
			break
		}
		if errs.Append(err) {
			continue
		}

		var row ExportRow
		err = row.Read(line)
		if errs.Append(err) {
			continue
		}

		hit, err := row.Hit(site.ID)
		if errs.Append(err) {
			continue
		}

		// Map session IDs to new session IDs.
		s, ok := sessions[row.Session]
		if !ok {
			sessions[row.Session] = Memstore.SessionID()
			s = sessions[row.Session]
		}
		hit.Session = s

		Memstore.Append(hit)
		n++

		// Spread out the load a bit.
		if cfg.Prod && n%5000 == 0 {
			time.Sleep(10 * time.Second)
		}
	}

	l.Debugf("imported %d rows", n)
	if errs.Len() > 0 {
		l.Error(errs)
	}

	if email {
		// Send email after 10s delay to make sure the cron task has finished
		// updating all the rows.
		time.Sleep(10 * time.Second)
		err = blackmail.Send("GoatCounter import ready",
			blackmail.From("GoatCounter import", cfg.EmailFrom),
			blackmail.To(user.Email),
			blackmail.BodyMustText(EmailTemplate("email_import_done.gotxt", struct {
				Site   Site
				Rows   int
				Errors *errors.Group
			}{*site, n, errs})))
		if err != nil {
			l.Error(err)
		}
	}
}

// TODO: would be nice to have generic csv marshal/unmarshaler, so you can do:
//
//    Path string `csv:"1"`
//
// Or something, or perhaps even get by header:
//
//    Path string `csv:"path"`
//
// Looks like there's some existing stuff for that already:
//
// https://github.com/gocarina/gocsv
// https://github.com/jszwec/csvutil

type ExportRow struct { // Fields in order!
	ID     int64 `db:"hit_id"`
	SiteID int64 `db:"site_id"`

	Path  string `db:"path"`
	Title string `db:"title"`
	Event string `db:"event"`

	UserAgent string `db:"ua"`
	Browser   string `db:"browser"`
	System    string `db:"system"`

	Session    zint.Uint128 `db:"session"`
	Bot        string       `db:"bot"`
	Ref        string       `db:"ref"`
	RefScheme  *string      `db:"ref_s"`
	Size       string       `db:"size"`
	Location   string       `db:"loc"`
	FirstVisit string       `db:"first"`
	CreatedAt  string       `db:"created_at"`
}

func (row *ExportRow) Read(line []string) error {
	const offset = 2 // Ignore first n fields

	values := reflect.ValueOf(row).Elem()
	if len(line) != values.NumField()-offset {
		return fmt.Errorf("wrong number of fields: %d (want: %d)", len(line), values.NumField()-offset)
	}

	for i := offset; i <= len(line)+1; i++ {
		f := values.Field(i)
		v := line[i-offset]

		switch f.Kind() {
		default:
		case reflect.Array:
			zi, _ := zint.ParseUint128(v, 16)
			f.Set(reflect.ValueOf(zi))
		case reflect.Ptr:
			f.Set(reflect.New(f.Type().Elem()))
			f.Elem().SetString(v)
		case reflect.String:
			f.SetString(v)
		}
	}

	return nil
}

func (row ExportRow) Hit(siteID int64) (Hit, error) {
	hit := Hit{
		Site:            siteID,
		Path:            row.Path,
		Title:           row.Title,
		Ref:             row.Ref,
		UserAgentHeader: row.UserAgent,
		Location:        row.Location, // TODO: validate from list?
	}

	v := zvalidate.New()
	v.Required("path", row.Path)
	hit.Event = zdb.Bool(v.Boolean("event", row.Event))
	hit.Bot = int(v.Integer("bot", row.Bot))
	hit.FirstVisit = zdb.Bool(v.Boolean("firstVisit", row.FirstVisit))
	hit.CreatedAt = v.Date("createdAt", row.CreatedAt, time.RFC3339)

	if unref(row.RefScheme) != "" {
		v.Include("refScheme", *row.RefScheme,
			[]string{*RefSchemeHTTP, *RefSchemeOther, *RefSchemeGenerated, *RefSchemeCampaign})
		hit.RefScheme = row.RefScheme
	}

	if row.Size != "" {
		err := hit.Size.UnmarshalText([]byte(row.Size))
		return hit, err
	}

	return hit, v.ErrorOrNil()
}

type ExportRows []ExportRow

// Export all hits for a site, including bot requests.
func (h *ExportRows) Export(ctx context.Context, limit, paginate int64) (int64, error) {
	if limit == 0 || limit > 5000 {
		limit = 5000
	}

	err := zdb.MustGet(ctx).SelectContext(ctx, h,
		`select * from hits_export where site_id=$1 and hit_id>$2 order by hit_id asc limit $3`,
		MustGetSite(ctx).ID, paginate, limit)

	hh := *h
	for i := range hh {
		hh[i].UserAgent = gadget.Unshorten(hh[i].UserAgent)
	}
	*h = hh

	last := paginate
	if len(*h) > 0 {
		hh := *h
		last = hh[len(hh)-1].ID
	}

	return last, errors.Wrap(err, "Hits.List")
}

func importError(l zlog.Log, user User, report error) {
	if e, ok := report.(*errors.StackErr); ok {
		report = e.Unwrap()
	}

	err := blackmail.Send("GoatCounter import error",
		blackmail.From("GoatCounter import", cfg.EmailFrom),
		blackmail.To(user.Email),
		blackmail.BodyMustText(EmailTemplate("email_import_error.gotxt", struct {
			Error error
		}{report})))
	if err != nil {
		l.Error(err)
	}
}
