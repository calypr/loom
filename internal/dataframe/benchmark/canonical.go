package benchmark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Result canonicalization for cross-backend comparison.
//
// The two backends legitimately differ in storage identity (Arango `_key`
// materialization vs the PostgreSQL stored logical id) and in JSON numeric
// spelling. A comparison is only meaningful under an EXPLICIT contract, so
// canonicalization here is total: every value maps to exactly one canonical
// token, null and missing are distinct, multiplicity is preserved (bag
// semantics), and unordered comparison sorts canonical row encodings.

// CanonicalValue is the closed canonical value algebra.
type CanonicalValue interface{ canonicalToken() string }

type CNull struct{}                          // JSON null
type CMissing struct{}                       // key absent from the row object
type CString string                          // string, exact bytes
type CBool bool                              // boolean
type CNumber struct{ Text string }           // numeric, canonical shortest spelling
type CList struct{ Items []CanonicalValue }  // ordered list, multiplicity kept
type CObject struct{ Pairs []CanonicalPair } // object, keys sorted on encode

type CanonicalPair struct {
	Key   string
	Value CanonicalValue
}

func (CNull) canonicalToken() string    { return "N" }
func (CMissing) canonicalToken() string { return "A" }
func (CString) canonicalToken() string  { return "" }
func (CBool) canonicalToken() string    { return "" }
func (CNumber) canonicalToken() string  { return "" }
func (CList) canonicalToken() string    { return "" }
func (CObject) canonicalToken() string  { return "" }

// canonicalEncode renders one deterministic token for a value. Type tags
// keep 1, "1", true, and null mutually distinct.
func canonicalEncode(v CanonicalValue) string {
	switch t := v.(type) {
	case CNull:
		return "N"
	case CMissing:
		return "A"
	case CString:
		return "s:" + strconv.Quote(string(t))
	case CBool:
		if t {
			return "b:1"
		}
		return "b:0"
	case CNumber:
		return "n:" + canonicalNumberText(t.Text)
	case CList:
		out := "l:["
		for i, item := range t.Items {
			if i > 0 {
				out += ","
			}
			out += canonicalEncode(item)
		}
		return out + "]"
	case CObject:
		pairs := append([]CanonicalPair(nil), t.Pairs...)
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
		out := "o:["
		for i, pair := range pairs {
			if i > 0 {
				out += ","
			}
			out += strconv.Quote(pair.Key) + ":" + canonicalEncode(pair.Value)
		}
		return out + "]"
	default:
		return "u:" + fmt.Sprintf("%T", v)
	}
}

// canonicalNumberText renders the shortest round-tripping decimal for the
// numeric spelling, so 1, 1.0, and 1e0 canonicalize identically while 0.1
// stays distinct from 0.10000000001.
func canonicalNumberText(text string) string {
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return "raw:" + text
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "raw:" + text
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// CanonicalizeValue converts one decoded JSON value. The caller distinguishes
// missing keys before calling (rows are walked with an explicit key set).
func CanonicalizeValue(value any) CanonicalValue {
	switch t := value.(type) {
	case nil:
		return CNull{}
	case bool:
		return CBool(t)
	case float64:
		return CNumber{Text: strconv.FormatFloat(t, 'g', -1, 64)}
	case json.Number:
		return CNumber{Text: string(t)}
	case string:
		return CString(t)
	case []any:
		items := make([]CanonicalValue, 0, len(t))
		for _, item := range t {
			items = append(items, CanonicalizeValue(item))
		}
		return CList{Items: items}
	case map[string]any:
		pairs := make([]CanonicalPair, 0, len(t))
		for key, child := range t {
			pairs = append(pairs, CanonicalPair{Key: key, Value: CanonicalizeValue(child)})
		}
		return CObject{Pairs: pairs}
	default:
		return CString(fmt.Sprintf("%v", t))
	}
}

// CanonicalRow is one row over the pinned column order. Missing keys become
// CMissing, so a backend that omits a null column cannot silently match one
// that emits null.
type CanonicalRow []CanonicalValue

// CanonicalizeRows converts row objects using the exact column order.
func CanonicalizeRows(columns []string, rows []map[string]any) []CanonicalRow {
	out := make([]CanonicalRow, 0, len(rows))
	for _, row := range rows {
		cr := make(CanonicalRow, len(columns))
		for i, column := range columns {
			value, ok := row[column]
			if !ok {
				cr[i] = CMissing{}
				continue
			}
			cr[i] = CanonicalizeValue(value)
		}
		out = append(out, cr)
	}
	return out
}

// CanonicalBag is a fully canonical unordered multiset of rows: the sorted
// canonical encodings. Equality of two bags is exactly cross-backend
// equality under this contract (types, multiplicity, and nested structure).
type CanonicalBag []string

func Bag(rows []CanonicalRow) CanonicalBag {
	bag := make(CanonicalBag, 0, len(rows))
	for _, row := range rows {
		tokens := make([]string, len(row))
		for i, cell := range row {
			tokens[i] = canonicalEncode(cell)
		}
		bag = append(bag, fmt.Sprintf("%q", tokens))
	}
	sort.Strings(bag)
	return bag
}

func (b CanonicalBag) Digest() string {
	h := sha256.New()
	for _, row := range b {
		h.Write([]byte(row))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FirstDifference reports the first row encoding where two equal-length
// bags diverge, for a human-readable mismatch report without dumping either
// full result set (bound values stay out of evidence).
func FirstDifference(a, b CanonicalBag) (string, string, bool) {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return truncateToken(a[i]), truncateToken(b[i]), true
		}
	}
	if len(a) != len(b) {
		return "(len " + strconv.Itoa(len(a)) + ")", "(len " + strconv.Itoa(len(b)) + ")", true
	}
	return "", "", false
}

func truncateToken(token string) string {
	const max = 160
	if len(token) <= max {
		return token
	}
	return token[:max] + "…"
}

// StorageIDMap is the explicit backend storage identity -> logical identity
// mapping the comparison uses. Backends must not be compared on storage
// identifiers; any column listed here is translated to logical identity on
// both sides before bagging.
//
// Arango rows carry storage keys of the form "Collection/key" or bare keys in
// row-identity columns; the PostgreSQL rows carry the stored logical id. The
// mapping is data-driven: the fixture manifest owns logical ids, and both
// spellings resolve through it, so a mismatch is a real mismatch.
type StorageIDMap struct {
	// Columns are the row columns that carry storage identities.
	Columns []string
	// toLogical maps either spelling (storage or logical) to the logical id.
	toLogical map[string]string
}

// NewStorageIDMap builds the mapping from fixture facts.
func NewStorageIDMap(facts *Facts) *StorageIDMap {
	toLogical := map[string]string{}
	add := func(logical string) {
		toLogical[logical] = logical
		// Arango spellings: Collection/key and _id-like Type/key forms.
		for _, collection := range []string{"Patient", "Observation", "Specimen", "Condition", "ResearchStudy"} {
			toLogical[collection+"/"+logical] = logical
		}
	}
	for _, p := range facts.Patients {
		add(p.ID)
		for _, id := range p.ObservationIDs {
			add(id)
		}
		for _, id := range p.SpecimenIDs {
			add(id)
		}
		for _, id := range p.ConditionIDs {
			add(id)
		}
	}
	for _, o := range facts.Observations {
		add(o.ID)
	}
	for _, s := range facts.Specimens {
		add(s.ID)
	}
	for _, c := range facts.Conditions {
		add(c.ID)
	}
	for _, s := range facts.StudyIDs {
		add(s)
	}
	return &StorageIDMap{
		Columns:   []string{"_row_id", "row_id", "resource_id", "root_resource_id"},
		toLogical: toLogical,
	}
}

// Apply rewrites identity-carrying cells through the logical mapping. An
// unmapped identity string stays verbatim (a foreign identity must compare
// unequal rather than vanish), and non-strings pass through.
func (m *StorageIDMap) Apply(columns []string, rows []map[string]any) []map[string]any {
	if m == nil || len(m.Columns) == 0 {
		return rows
	}
	present := map[string]bool{}
	for _, column := range columns {
		present[column] = true
	}
	watched := map[string]bool{}
	for _, column := range m.Columns {
		if present[column] {
			watched[column] = true
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		clone := make(map[string]any, len(row))
		for key, value := range row {
			if watched[key] {
				if text, ok := value.(string); ok {
					if logical, found := m.toLogical[text]; found {
						clone[key] = logical
						continue
					}
				}
			}
			clone[key] = value
		}
		out = append(out, clone)
	}
	return out
}
