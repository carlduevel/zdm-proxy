package filter

import (
	"encoding/binary"
	"strings"
	"time"

	"cloud-gate/migration/migration"
)

const (
	selectQuery   = QueryType("select")
	useQuery      = QueryType("use")
	insertQuery   = QueryType("insert")
	updateQuery   = QueryType("update")
	deleteQuery   = QueryType("delete")
	truncateQuery = QueryType("truncate")
	prepareQuery  = QueryType("prepare")
	miscQuery     = QueryType("misc")
)

type QueryType string

type Query struct {
	Timestamp uint64
	Stream    uint16
	Table     *migration.Table

	Type  QueryType
	Query []byte
}

func newQuery(table *migration.Table, queryType QueryType, query []byte) *Query {
	return &Query{
		Timestamp: uint64(time.Now().UnixNano() / 1000000),
		Table:     table,
		Type:      queryType,
		Query:     query,
		Stream:    binary.BigEndian.Uint16(query[2:4]),
	}
}

// TODO: Handle Batch statements. Currently assumes Query is QUERY or EXECUTE
// usingTimestamp will add a timestamp within the query, if one is not already present.
func (q *Query) usingTimestamp() *Query {
	opcode := q.Query[4]

	//index represents start of <query_parameters> in binary protocol
	var index int
	if opcode == 0x07 {
		//if QUERY
		queryLen := binary.BigEndian.Uint32(q.Query[9:13])
		index = 13 + int(queryLen)
	} else if opcode == 0x0a {
		//if EXECUTE
		queryLen := binary.BigEndian.Uint32(q.Query[9:11])
		index = 11 + int(queryLen)
	}

	// Query already includes timestamp, ignore
	// Byte 0x20 of the flags portion of the query represent whether or not a timestamp
	// will be included with this query
	if q.Query[index+2]&0x20 == 0x20 {
		// TODO: Ensure we can keep the original timestamp & we don't need to alter anything
		// binary.BigEndian.PutUint64(q.Query[len(q.Query) - 8:], q.Timestamp)
		return q
	}

	// Set the timestamp bit (0x20) of flags to 1
	q.Query[index+2] = q.Query[index+2] | 0x20

	// Add timestamp to end of query
	timestamp := make([]byte, 8)
	binary.BigEndian.PutUint64(timestamp, q.Timestamp)
	q.Query = append(q.Query, timestamp...)

	// Update length of body
	bodyLen := binary.BigEndian.Uint32(q.Query[5:9]) + 8
	binary.BigEndian.PutUint32(q.Query[5:9], bodyLen)

	return q
}

// TODO: Make cleaner / more efficient
// addKeyspace will explicity add the keyspace to a query, if not present.
// For example, if the user is in keyspace 'codebase' and they run:
// 		INSERT INTO tasks(id, task) VALUES(now(), 'task')
// this function will change the query to
// 		INSERT INTO codebase.tasks(id, task) VALUES(now(), 'task')
func (q *Query) addKeyspace(keyspace string) *Query {
	// Find table in original query
	index := strings.Index(string(q.Query), q.Table.Name)

	before := make([]byte, index)
	copy(before, q.Query[:index])
	after := q.Query[index:]

	// Rebuild query
	tablePrefix := []byte(keyspace + ".")
	updatedQuery := append(before, tablePrefix...)
	updatedQuery = append(updatedQuery, after...)

	// Update query length
	bodyLen := binary.BigEndian.Uint32(updatedQuery[5:9]) + uint32(len(tablePrefix))
	binary.BigEndian.PutUint32(updatedQuery[5:9], bodyLen)

	q.Query = updatedQuery

	return q
}
