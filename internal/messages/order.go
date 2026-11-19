package messages

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// occurredLayout is fixed width and always UTC, so the occurred column sorts
// as text in time order and SQLite can order, page and find the latest
// message through its indexes. RFC3339Nano drops trailing zeros, which made
// "…:05Z" sort after "…:05.5Z".
const occurredLayout = "2006-01-02T15:04:05.000000000Z"

func occurredKey(t time.Time) string { return t.UTC().Format(occurredLayout) }

// migrateOrder brings a database from before this layout up to date: it
// rewrites occurred values in the old layout and adds the indexed outgoing
// column the queue reads. Both steps only touch what is missing.
func migrateOrder(db *sql.DB) error {
	var has int
	if e := db.QueryRow("SELECT count(*) FROM pragma_table_xinfo('messages') WHERE name='outgoing'").Scan(&has); e != nil {
		return e
	}
	if has == 0 {
		if _, e := db.Exec(`ALTER TABLE messages ADD COLUMN outgoing INTEGER GENERATED ALWAYS AS (coalesce(json_extract(payload,'$.outgoing'),0)) VIRTUAL`); e != nil {
			return e
		}
	}
	if _, e := db.Exec(`CREATE INDEX IF NOT EXISTS message_latest ON messages(conversation, outgoing, occurred)`); e != nil {
		return e
	}
	rows, e := db.Query(fmt.Sprintf("SELECT rowid, occurred, payload FROM messages WHERE length(occurred) != %d OR substr(occurred, -1) != 'Z'", len(occurredLayout)))
	if e != nil {
		return e
	}
	type fix struct {
		rowid int64
		key   string
	}
	var fixes []fix
	for rows.Next() {
		var id int64
		var old, raw string
		if e = rows.Scan(&id, &old, &raw); e != nil {
			rows.Close()
			return e
		}
		t, err := time.Parse(time.RFC3339Nano, old)
		if err != nil {
			m, err := decodeMessage(raw)
			if err != nil {
				rows.Close()
				return fmt.Errorf("message %d has an unreadable time %q", id, old)
			}
			t = m.At
		}
		fixes = append(fixes, fix{id, occurredKey(t)})
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(fixes) == 0 {
		return e
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, f := range fixes {
		if _, e = tx.Exec("UPDATE messages SET occurred=? WHERE rowid=?", f.key, f.rowid); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// orderKey is a message's place in its conversation: time, then arrival.
// Equal times keep ingestion order, as the queue always has.
const orderKey = "occurred || '#' || printf('%019d', rowid)"

// latestKeys maps each conversation to the order key of its latest message,
// or latest incoming message, read from the index alone.
func latestKeys(q querier2, incomingOnly bool) (map[string]string, error) {
	where := ""
	if incomingOnly {
		where = " WHERE outgoing = 0"
	}
	rows, e := q.Query("SELECT conversation, max(" + orderKey + ") FROM messages" + where + " GROUP BY conversation")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var conv, key string
		if e = rows.Scan(&conv, &key); e != nil {
			return nil, e
		}
		out[conv] = key
	}
	return out, rows.Err()
}

type querier2 interface {
	Query(string, ...any) (*sql.Rows, error)
}

func splitOrderKey(key string) (time.Time, int64, error) {
	i := strings.LastIndexByte(key, '#')
	if i < 0 {
		return time.Time{}, 0, fmt.Errorf("bad order key %q", key)
	}
	t, e := time.Parse(occurredLayout, key[:i])
	if e != nil {
		return t, 0, e
	}
	id, e := strconv.ParseInt(key[i+1:], 10, 64)
	return t, id, e
}

func messageAt(q querier, rowid int64) (Message, error) {
	return getJSON[Message](q, "SELECT payload FROM messages WHERE rowid=?", rowid)
}
