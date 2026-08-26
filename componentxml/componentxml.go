// Package componentxml is the struct view of Boomi component XML — the
// one place in this SDK allowed to interpret it.
//
// The rest of the SDK treats component XML as opaque bytes on principle:
// a typed round-trip through Go's encoding/xml normalises namespaces,
// attribute order, and self-closing forms, and a component written back
// through a lossy view is a corrupted component. This package moves that
// line deliberately, and only as far as it can be held:
//
//   - The ENVELOPE (<bns:Component> attributes, description,
//     encryptedValues) is typed. It follows a stable platform ruleset
//     and re-authoring it is safe.
//   - The OBJECT (<bns:object> — the actual component definition:
//     process shapes, connector configuration, profiles) is captured
//     verbatim and re-emitted byte-for-byte. It is never decoded,
//     reordered, or re-encoded.
//
// Re-encoding a decoded component therefore reproduces the inner object
// exactly, while the envelope is rewritten in normalised, semantically
// equivalent form (default namespace instead of a bns: prefix, canonical
// attribute order). Callers that need the whole document byte-for-byte —
// diffing, hashing, sync-state comparison — should stay on the raw
// streams (objects.Components, objects.Raw), which remain the default.
package componentxml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// Namespace is the platform API's XML namespace.
const Namespace = "http://api.platform.boomi.com/"

// EncryptedValue marks one encrypted field inside the component object.
// The platform never discloses the value itself; Path locates the field
// and IsSet reports whether a value exists on the platform side.
type EncryptedValue struct {
	Path  string `xml:"path,attr"`
	IsSet bool   `xml:"isSet,attr"`
}

// RawSubtree carries an XML subtree verbatim: decoded bytes are captured
// exactly as they appeared and re-emitted untouched on encode.
type RawSubtree struct {
	InnerXML []byte `xml:",innerxml"`
}

// Component is the typed envelope of one component document.
//
// Zero-valued optional attributes are omitted on encode, so a struct
// authored for a create (no componentId, no version) marshals to the
// create form the platform expects.
type Component struct {
	XMLName xml.Name `xml:"http://api.platform.boomi.com/ Component"`

	ComponentID    string `xml:"componentId,attr,omitempty"`
	Version        int    `xml:"version,attr,omitempty"`
	Name           string `xml:"name,attr,omitempty"`
	Type           string `xml:"type,attr,omitempty"`
	SubType        string `xml:"subType,attr,omitempty"`
	FolderID       string `xml:"folderId,attr,omitempty"`
	FolderFullPath string `xml:"folderFullPath,attr,omitempty"`
	FolderName     string `xml:"folderName,attr,omitempty"`
	BranchID       string `xml:"branchId,attr,omitempty"`
	CreatedDate    string `xml:"createdDate,attr,omitempty"`
	CreatedBy      string `xml:"createdBy,attr,omitempty"`
	ModifiedDate   string `xml:"modifiedDate,attr,omitempty"`
	ModifiedBy     string `xml:"modifiedBy,attr,omitempty"`
	Deleted        bool   `xml:"deleted,attr,omitempty"`
	CurrentVersion bool   `xml:"currentVersion,attr,omitempty"`

	EncryptedValues []EncryptedValue `xml:"encryptedValues>encryptedValue"`
	Description     string           `xml:"description,omitempty"`

	// Object is the component definition, verbatim. Nil means the
	// document carried no <bns:object> block.
	Object *RawSubtree `xml:"object"`

	// ProcessOverrides is carried verbatim when present; most
	// components have none.
	ProcessOverrides *RawSubtree `xml:"processOverrides,omitempty"`
}

// ObjectXML returns the inner object bytes, or nil when the document has
// no object block.
func (c *Component) ObjectXML() []byte {
	if c.Object == nil {
		return nil
	}

	return c.Object.InnerXML
}

// SetObjectXML replaces the component definition with the given subtree,
// carried verbatim into the next encode.
func (c *Component) SetObjectXML(inner []byte) {
	c.Object = &RawSubtree{InnerXML: inner}
}

// Decode parses one component document from r. The envelope becomes
// struct fields; the object subtree is captured byte-for-byte.
func Decode(r io.Reader) (*Component, error) {
	var c Component
	if err := xml.NewDecoder(r).Decode(&c); err != nil {
		return nil, fmt.Errorf("componentxml: decoding component: %w", err)
	}

	return &c, nil
}

// DecodeBytes is Decode over a byte slice.
func DecodeBytes(doc []byte) (*Component, error) {
	return Decode(bytes.NewReader(doc))
}

// Encode writes the component as a platform-accepted XML document: the
// standard header, the envelope in the platform namespace (default-ns
// form), and the object subtree verbatim.
func (c *Component) Encode(w io.Writer) error {
	if c.Name == "" && c.ComponentID == "" {
		return errors.New("componentxml: a component needs a name (create) or a componentId (update)")
	}

	if _, err := io.WriteString(w, xml.Header); err != nil {
		return fmt.Errorf("componentxml: writing header: %w", err)
	}

	enc := xml.NewEncoder(w)
	enc.Indent("", "    ")

	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("componentxml: encoding component: %w", err)
	}

	if err := enc.Close(); err != nil {
		return fmt.Errorf("componentxml: flushing encoder: %w", err)
	}

	return nil
}

// Marshal renders the component document as bytes — the form the
// Components service's Create and Update take (wrap in bytes.NewReader,
// which retries can rewind).
func (c *Component) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	if err := c.Encode(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
