package provenance

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Options configures Register.
type Options struct {
	skipTables    map[string]bool
	skipPrefixes  []string
	correlationID func(*gorm.Statement) string
	log           *slog.Logger
}

// Option mutates Options.
type Option func(*Options)

// WithSkipTables never records changes of these tables (high-volume or
// technical tables). Repeatable.
func WithSkipTables(tables ...string) Option {
	return func(o *Options) {
		for _, t := range tables {
			if t = strings.TrimSpace(t); t != "" {
				o.skipTables[t] = true
			}
		}
	}
}

// WithSkipPrefixes skips every table whose name starts with one of the
// prefixes. Repeatable.
func WithSkipPrefixes(prefixes ...string) Option {
	return func(o *Options) { o.skipPrefixes = append(o.skipPrefixes, prefixes...) }
}

// WithCorrelation extracts a correlation id (request id) from the statement.
func WithCorrelation(fn func(*gorm.Statement) string) Option {
	return func(o *Options) { o.correlationID = fn }
}

// WithLogger sets the logger for best-effort failures.
func WithLogger(l *slog.Logger) Option {
	return func(o *Options) {
		if l != nil {
			o.log = l
		}
	}
}

// DefaultSkipTables are technical tables the hook never records.
var DefaultSkipTables = []string{
	TableName, "audit_entries", "activity_logs", "metacore_outbox", "outbox_events",
	"sessions", "user_sessions", "refresh_tokens", "notifications_deliveries",
}

// DefaultSkipPrefixes are technical prefixes the hook never records
// (kernel bookkeeping tables, schema migrations).
var DefaultSkipPrefixes = []string{"metacore_", "schema_migrations", "goose_", "dispatch_", "idempotency"}

const callbackPrefix = "metacore:provenance:"

// Register installs the provenance callbacks on db and migrates the lateral
// table. Idempotent: calling it twice replaces the callbacks.
//
// For every Create / Update / Delete whose context carries a Provenance:
//
//   - before the write, created_by_id (create, when empty) and updated_by_id
//     are stamped with Provenance.Accountable() — only on models that already
//     declare those fields, never adding columns and never with a fake id;
//   - after a successful write, one Entry per affected record goes to
//     metacore_provenance in the same transaction (under a savepoint, so a
//     failure there is logged and never aborts the business write).
//
// Writes without a Provenance in ctx are untouched.
func Register(db *gorm.DB, opts ...Option) error {
	if db == nil {
		return fmt.Errorf("provenance: nil db")
	}
	o := &Options{skipTables: map[string]bool{}, log: slog.Default()}
	for _, t := range DefaultSkipTables {
		o.skipTables[t] = true
	}
	o.skipPrefixes = append(o.skipPrefixes, DefaultSkipPrefixes...)
	for _, opt := range opts {
		opt(o)
	}
	if err := Migrate(db); err != nil {
		return err
	}
	h := &hook{opts: o}
	cb := db.Callback()
	steps := []struct {
		name string
		get  func(string) func(*gorm.DB)
		repl func(string, func(*gorm.DB)) error
		reg  func(string, func(*gorm.DB)) error
		fn   func(*gorm.DB)
	}{
		{"stamp_create", cb.Create().Get, cb.Create().Replace, cb.Create().After("gorm:before_create").Before("gorm:create").Register, h.stampCreate},
		{"record_create", cb.Create().Get, cb.Create().Replace, cb.Create().After("gorm:create").Before("gorm:commit_or_rollback_transaction").Register, h.recordAfter(ActionCreated)},
		{"stamp_update", cb.Update().Get, cb.Update().Replace, cb.Update().After("gorm:before_update").Before("gorm:update").Register, h.stampUpdate},
		{"record_update", cb.Update().Get, cb.Update().Replace, cb.Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register, h.recordAfter(ActionUpdated)},
		{"record_delete", cb.Delete().Get, cb.Delete().Replace, cb.Delete().After("gorm:delete").Before("gorm:commit_or_rollback_transaction").Register, h.recordAfter(ActionDeleted)},
	}
	for _, st := range steps {
		name := callbackPrefix + st.name
		var err error
		if st.get(name) != nil {
			err = st.repl(name, st.fn)
		} else {
			err = st.reg(name, st.fn)
		}
		if err != nil {
			return fmt.Errorf("provenance: register %s: %w", st.name, err)
		}
	}
	return nil
}

// Registered reports whether Register installed the callbacks on db (code
// that records changes explicitly uses it to stay silent on hosts that did
// not opt in).
func Registered(db *gorm.DB) bool {
	return db != nil && db.Callback().Create().Get(callbackPrefix+"record_create") != nil
}

type hook struct {
	opts *Options
}

func (h *hook) skip(table string) bool {
	if table == "" || h.opts.skipTables[table] {
		return true
	}
	for _, p := range h.opts.skipPrefixes {
		if p != "" && strings.HasPrefix(table, p) {
			return true
		}
	}
	return false
}

func provenanceOf(db *gorm.DB) (Provenance, bool) {
	if db == nil || db.Statement == nil {
		return Provenance{}, false
	}
	return FromContext(db.Statement.Context)
}

func tableOf(stmt *gorm.Statement) string {
	if stmt.Table != "" {
		return stmt.Table
	}
	if stmt.Schema != nil {
		return stmt.Schema.Table
	}
	return ""
}

// stampCreate fills created_by_id / updated_by_id when empty.
func (h *hook) stampCreate(db *gorm.DB) {
	if db.Error != nil {
		return
	}
	p, ok := provenanceOf(db)
	if !ok || h.skip(tableOf(db.Statement)) {
		return
	}
	who := p.Accountable()
	if who == nil {
		return
	}
	stmt := db.Statement
	if stmt.Schema == nil {
		stampMaps(stmt.Dest, *who, false)
		return
	}
	for _, col := range []string{"created_by_id", "updated_by_id"} {
		f := stmt.Schema.LookUpField(col)
		if f == nil {
			continue
		}
		eachStruct(stmt.ReflectValue, func(rv reflect.Value) {
			if _, zero := f.ValueOf(stmt.Context, rv); zero {
				_ = f.Set(stmt.Context, rv, *who)
			}
		})
	}
}

// stampUpdate stamps updated_by_id with the accountable person.
func (h *hook) stampUpdate(db *gorm.DB) {
	if db.Error != nil {
		return
	}
	p, ok := provenanceOf(db)
	if !ok || h.skip(tableOf(db.Statement)) {
		return
	}
	who := p.Accountable()
	if who == nil {
		return
	}
	stmt := db.Statement
	if stmt.Schema == nil {
		stampMaps(stmt.Dest, *who, true)
		return
	}
	if f := stmt.Schema.LookUpField("updated_by_id"); f != nil {
		if m, isMap := stmt.Dest.(map[string]any); isMap {
			if _, set := m["updated_by_id"]; !set {
				m["updated_by_id"] = *who
			}
			return
		}
		stmt.SetColumn("updated_by_id", *who, true)
	}
}

// stampMaps handles schema-less map writes (Table(x).Create(map)): only keys
// the caller already put are kept; created_by_id is filled only when the map
// declares the column (present with a nil/zero value), since a schema-less
// write cannot know whether the column exists.
func stampMaps(dest any, who uuid.UUID, update bool) {
	fill := func(m map[string]any) {
		for _, col := range []string{"created_by_id", "updated_by_id"} {
			if update && col == "created_by_id" {
				continue
			}
			v, present := m[col]
			if present && isBlank(v) {
				m[col] = who
			}
		}
	}
	switch d := dest.(type) {
	case map[string]any:
		fill(d)
	case *map[string]any:
		if d != nil {
			fill(*d)
		}
	case []map[string]any:
		for _, m := range d {
			fill(m)
		}
	}
}

func isBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case uuid.UUID:
		return x == uuid.Nil
	case *uuid.UUID:
		return x == nil || *x == uuid.Nil
	case string:
		return strings.TrimSpace(x) == "" || x == uuid.Nil.String()
	}
	return false
}

// recordAfter appends one Entry per affected record.
func (h *hook) recordAfter(action string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if db.Error != nil || db.RowsAffected == 0 {
			return
		}
		p, ok := provenanceOf(db)
		if !ok {
			return
		}
		stmt := db.Statement
		table := tableOf(stmt)
		if h.skip(table) {
			return
		}
		recs := affected(stmt)
		if len(recs) == 0 {
			return
		}
		now := time.Now().UTC()
		corr := ""
		if h.opts.correlationID != nil {
			corr = h.opts.correlationID(stmt)
		}
		entries := make([]Entry, 0, len(recs))
		for _, r := range recs {
			e := NewEntry(r.org, table, r.id, action, p, now)
			e.CorrelationID = corr
			entries = append(entries, e)
		}
		h.persist(db, table, action, entries)
	}
}

func (h *hook) persist(db *gorm.DB, table, action string, entries []Entry) {
	tx := db.Session(&gorm.Session{NewDB: true, Context: Without(db.Statement.Context)})
	_, inTx := db.Statement.ConnPool.(gorm.TxCommitter)
	if inTx {
		sp := "mc_prov_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
		if err := tx.SavePoint(sp).Error; err != nil {
			h.opts.log.Warn("provenance: savepoint", "table", table, "err", err)
			return
		}
		if err := record(tx, entries); err != nil {
			_ = tx.RollbackTo(sp).Error
			h.opts.log.Warn("provenance: record failed (write kept)", "table", table, "action", action, "err", err)
		}
		return
	}
	if err := record(tx, entries); err != nil {
		h.opts.log.Warn("provenance: record failed (write kept)", "table", table, "action", action, "err", err)
	}
}

type recRef struct {
	id  string
	org uuid.UUID
}

// affected lists the records a statement touched: struct(s) with a primary
// key value, or map(s) carrying "id". Statements that update by condition
// only (no primary key known) are skipped — those callers should write
// through a model value or record the change explicitly with Store.Record.
func affected(stmt *gorm.Statement) []recRef {
	var out []recRef
	if stmt.Schema != nil && stmt.Schema.PrioritizedPrimaryField != nil {
		pk := stmt.Schema.PrioritizedPrimaryField
		orgF := stmt.Schema.LookUpField("organization_id")
		eachStruct(stmt.ReflectValue, func(rv reflect.Value) {
			v, zero := pk.ValueOf(stmt.Context, rv)
			if zero {
				return
			}
			r := recRef{id: fmt.Sprint(v)}
			if orgF != nil {
				if ov, z := orgF.ValueOf(stmt.Context, rv); !z {
					r.org = asUUID(ov)
				}
			}
			out = append(out, r)
		})
		if len(out) > 0 {
			return out
		}
	}
	fromMap := func(m map[string]any) {
		if m == nil {
			return
		}
		id, ok := m["id"]
		if !ok || isBlank(id) {
			return
		}
		out = append(out, recRef{id: fmt.Sprint(id), org: asUUID(m["organization_id"])})
	}
	switch d := stmt.Dest.(type) {
	case map[string]any:
		fromMap(d)
	case *map[string]any:
		if d != nil {
			fromMap(*d)
		}
	case []map[string]any:
		for _, m := range d {
			fromMap(m)
		}
	}
	return out
}

func asUUID(v any) uuid.UUID {
	switch x := v.(type) {
	case uuid.UUID:
		return x
	case *uuid.UUID:
		if x != nil {
			return *x
		}
	case string:
		if id, err := uuid.Parse(x); err == nil {
			return id
		}
	case []byte:
		if id, err := uuid.ParseBytes(x); err == nil {
			return id
		}
	}
	return uuid.Nil
}

// eachStruct visits the struct value(s) of a statement (single or slice).
func eachStruct(rv reflect.Value, fn func(reflect.Value)) {
	for rv.IsValid() && (rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Struct:
		fn(rv)
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			e := rv.Index(i)
			for e.Kind() == reflect.Ptr || e.Kind() == reflect.Interface {
				if e.IsNil() {
					break
				}
				e = e.Elem()
			}
			if e.Kind() == reflect.Struct {
				fn(e)
			}
		}
	}
}
