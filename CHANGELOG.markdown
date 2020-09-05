ChangeLog for GoatCounter
=========================

This list is not comprehensive, and only lists new features and major changes,
but not every minor bugfix.

The goatcounter.com service generally runs the latest master.

Unreleased v1.5.0
-----------------

This release contains quite a few changes to the database layout to make
everything faster and reduce the size on disk.

1. Run the migrations with `goatcounter serve -automigrate` or `goatcounter
   migrate`.

2. You may want to run `VACUUM` (or `VACUUM FULL` for PostgreSQL) manually after
   the migration to free up unused rows. This can't be done automatically from a
   transaction.

3. Run `goatcounter reindex`.

This may take a while if you've got a lot of data. For about 500,00 pageviews it
takes about 3 minutes, but if you've got millions of rows it may take an hour or
more.


2020-09-04 v1.4.1
-----------------

A few small updates, fixes, and performance enhancements. Nothing major.

You can see a list of changes in the git log:
https://github.com/zgoat/goatcounter/compare/v1.4.0...v1.4.1


2020-08-24 v1.4.0
-----------------

- **Change defaults for `-listen`** (#336)

  The default for the `-listen` flag changed from `localhost:8081` to `:443`,
  which is probably a better and less confusing default for most people. There
  is also some more detailed docs available in `goatcounter help listen`.

- Set Cache-Control header for static files (#348)

  The `Cache-Control` header is now set for static files. Since the "cache
  busting" happens based on the goatcounter version it's now recommended to set
  this if you're compiling GoatCounter yourself. See the updated README for
  instructions.

- Add multi-factor auth (#306)

  TOTP-based multi-factor auth is now supported.

- Better export, export API, add import feature (#316, #318, #329)

  You can now import the CSV exports, useful for migrating from self-hosted to
  goatcounter.com or vice versa, or for migrating from other systems. There is a
  web interface and a `goatcounter import` command.

  The export now supports a "pagination cursor", so you can export only rows you
  didn't previously export. This is especially useful with the new export API.
  which should make it easy to sync GoatCounter data with another external
  platform.

  See http://goatcounter.com/api for details on the export API.

- API for sending pageviews (#357)

  Doing that with the regular `/count` is actually quite painful, as you quickly
  run in to ratelimits, need to set specific headers, etc. Adding an API
  endpoint for that makes things much easier.

- API for creating and editing additional sites (#361)

- Some redesigns (#324, #315, #321 #320)

  The "Totals" is now placed below the Pages; I think it makes more sense there.
  The Y-axis for the totals is now also independent. There's also been a quite a
  few restylings.

- Add "text view" mode (#359)

  View your data as a simple table without too much graphics; only the main
  "Pages" overview is implemented for now.

- Make it easier to skip your own views (#290)

  Previously this required adding custom code, but now loading any page with
  `#toggle-goatcounter` added will enable/disable the GoatCounter tracking for
  that browser.

- Can now manage "additional sites" from self-hosted GoatCounter (#363)

  This wasn't possible before for no other reason than laziness on my part 🙃

- public/count.js is now ISC licensed (#309)

  Previously the EUPL applied, which is fairly restrictive and may prevent
  people from including/self-hosting the count.js script.

- Add `goatcounter db` command

  This is mostly useful for writing deploy scripts: `goatcounter db
  schema-sqlite` prints the SQLite schema, `schema-pgsql` prints the PostgreSQL
  schema, and `goatcounter db test` tests if the database exists.

- Session hashes are no longer persisted to the database

  This is kind of an internal change, but session hashes are now stored in
  memory only and never recorded to the database. There's no real reason to
  persistently store this information, and this is a (small) privacy/GDPR
  compliance improvement.


2020-06-01 v1.3.0
-----------------

Note: this release contains quite a few database migrations; they make take a
minute to run (depending on your table size), and you may want to run a `VACUUM`
afterwards.

- Remove email auth, replace `-auth` with `-email-from` (#263, #270)

  As mentioned in the 1.2 release the email authentication is now removed. You
  can still reset the password for old accounts.

  Since the email auth no longer exists the `-auth` parameter no longer makes
  sense. It's now replaced with `-email-from`, which can be set to just an email
  address.

  **Action required**: if you set the email address with `-auth` you'll have to
  change it to `-email-from`.

- Add OS stats, improve accuracy of browser stats (#261)

  GoatCounter now tracks the OS/platform in addition to just the browser, and
  the accuracy of the browser stats should be improved.

  **Action required**: you'll need to populate the `system_stats` table:

      $ goatcounter reindex -table system_stats

  If you want to process all browser stats with the new logic too, then use this
  instead:

      $ goatcounter reindex -table system_stats,browser_stats

- Improve performance (#265, #273, #274)

  Increase performance by quite a bit on large sites and time ranges.

- Remove the per-path scaling (#267)

  Previously GoatCounter would scale the Y-axis different for every path in the
  dashboard, but this was more confusing than helpful. It's now always scaled to
  the maximum of all paths in the selected date range and filter, with a field
  to scale it lower on-demand if desired.

- Add totals overview (#271)

  Add chart with totals for the selected date range and filter.

- Add `goatcounter.url()`, `goatcounter.filter()` (#272, #253)

  Adds two new methods to the `count.js` script so it's easier to use write own
  implementation. In addition the script will now issue a `console.warn()` if a
  request isn't being counted for some reason.


2020-05-18 v1.2.0
-----------------

There are a number of changes in 1.2, and a few which require a bit of action
when updating. Also see: https://www.arp242.net/goatcounter-1.2.html

- Password authentication (#232)

  The email-based authentication has been deprecated in favour of password
  authentication.

  **Action required** Use the interface to set a password (you will get a
  notification about this). Email authentication still works, but will be
  removed in the next release, after which updating the password will be tricky.

- Unique visit tracking (#212)

  GoatCounter now tracks unique visits (without using cookies).

  Technical documentation about the implementation is in
  [doc/sessions.markdown](doc/sessions.markdown).

  There are two ways to display the older stats:

  1. Do nothing; meaning that "visits" will be 0 for previous date ranges.

  2. Assign a new 'session' to every hit, so that unique visits will be the same
     as the number of pageviews.

  Doing option 2 is a potentially expensive database operation and not everyone
  may care so it's not done automatically; instructions for doing this are:

  - SQLite (do *not* do this on a running system; as far as I can tell there's
    no good way to get the next sequence ID while incrementing it):

        delete from sessions;
        update hits set session=id, first_visit=1;
        update sqlite_sequence set seq = (select max(session) from hits) where name='sessions';

  - PostgreSQL:

        update hits set session=nextval('sessions_id_seq'), first_visit=1;

  And then run `goatcounter reindex`.

- Improve bot detection (#219)

  The bot detection is now improved; this will be applied to older pageviews in
  the database with a migration, but the cached statistics aren't updated
  automatically (as it can take a while for larger sites). Use the `reindex`
  command to fully update older pageviews (this is entirely optional).

- Track events (#215)

  There is now better support to track events; see the updated documentation on
  the Site Code page for details.

- Better support for campaigns (#238)

  There is now a "campaign parameters" setting; if the URL matches one of these
  parameters it will be set as the referrer (overriding the `Referer` header).

  The default is `utm_campaign, utm_source, ref`.

- Better export (#221)

  The export was a quick feature added in the first version, but didn't scale
  well to larger sites with a lot of pageviews. This now works well for any
  number of pageviews.

- Many small improvements and bug fixes

  It's almost 2 months of work, and there have been many small changes, fixes,
  and improvements. I didn’t keep track of them all 😅


2020-03-27 v1.1.2
-----------------

- Fix small issue with the domain not showing correct in the site code 😅


2020-03-27 v1.1.1
-----------------

- Small bugfix release which fixes some small issues and improves a few small
  documentation issues. List of changes:
  https://github.com/zgoat/goatcounter/compare/v1.1.0...v1.1.1

- The biggest change is that the `saas` command no longer works (and is no
  longer documented). It was only ever useful for hosting goatcounter.com, and
  has a number of assumptions and hard-coded values.

  If you're using `saas`, then you can migrate to `serve` by setting a custom
  domain (`sites.cname`) for all the sites. The `serve` command should work
  after that.


2020-03-18 v1.1.0
-----------------

- **Incompatible** Improve CLI UX (#154, #173, #175, #181)

  The entire CLI has been redone; the original wasn't very user-friendly for
  self-hosting. See `goatcounter help` for the full docs, but in brief:

      o Use "goatcounter serve" instead of just "goatcounter".
      o Create new sites with "goatcounter create".
      o Good support for TLS hosting and ACME certificates (see -tls flag).
      o Invert -prod to -dev (i.e. just drop -prod for production services, add -dev for development).
      o -smtp flag is no longer required.
      o -dbconnect                 →  -db
      o -pgsql                     →  -db postgresql://...
      o -staticdomain              →  no longer needed, but if you really want it you can
                                      append to domain: -domain example.com,static.example.com
      o -emailerrors               →  -errors mailto:...
      o goatcounter -migrate       →  goatcounter migrate
      o goatcounter -migrate auto  →  goatcounter serve -automigrate

- **Action required** Show top referrals (#192)

  To populate the ref_stats and size_stats tables for older data, update first
  and then run:

      $ goatcounter reindex -confirm -table ref_stats
      $ goatcounter reindex -confirm -table size_stats

- Charts are displayed in local timezone (#155)

- Add "IgnoreIPs" setting to ignore your own views (#128)

- Link to paths by adding a new domain setting (#138)

- Add configurable data retention (#134)

- Allow configuring the thousands separator (#132)

- Allow filtering pages in the dashboard (#106)

- Improve the integration code (#122)

- Allow sending emails without a relay (#184)

- Add get_query() to count.js to get query parameter (#199)

- Allow viewing the charts by day, instead of only by hour (#169)


2020-01-13 v1.0.0
-----------------

Initial release.
