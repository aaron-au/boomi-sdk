package componentxml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aaron-au/boomi-sdk/componentxml"
)

// sample is the platform's own prefixed form, with an inner object whose
// exact bytes — attribute order, self-closing forms, comments — must
// survive untouched.
const sample = `<?xml version="1.0" encoding="UTF-8"?>
<bns:Component xmlns:bns="http://api.platform.boomi.com/"
               componentId="abc-123"
               version="7"
               name="Order Intake"
               type="process"
               folderId="f-1"
               folderFullPath="Home/Orders"
               branchId="Qjo1"
               deleted="false"
               currentVersion="true"
               createdBy="dev@example.com">
  <bns:encryptedValues>
    <bns:encryptedValue isSet="true" path="//GenericConnectionConfig/field[@type='password']"/>
  </bns:encryptedValues>
  <bns:description>Takes orders in.</bns:description>
  <bns:object>
    <Operation zOrder="b" aOrder="a">
      <!-- a comment the platform kept -->
      <Configuration enabled="false"/>
    </Operation>
  </bns:object>
</bns:Component>`

func TestDecodeEnvelopeAndVerbatimObject(t *testing.T) {
	c, err := componentxml.DecodeBytes([]byte(sample))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}

	if c.ComponentID != "abc-123" || c.Version != 7 || c.Name != "Order Intake" || c.Type != "process" {
		t.Fatalf("envelope = %+v, want abc-123 v7 process", c)
	}

	if !c.CurrentVersion || c.Deleted {
		t.Fatalf("flags = current %v deleted %v, want true/false", c.CurrentVersion, c.Deleted)
	}

	if c.Description != "Takes orders in." {
		t.Fatalf("description = %q", c.Description)
	}

	if len(c.EncryptedValues) != 1 || !c.EncryptedValues[0].IsSet ||
		!strings.Contains(c.EncryptedValues[0].Path, "password") {
		t.Fatalf("encryptedValues = %+v", c.EncryptedValues)
	}

	object := string(c.ObjectXML())
	// Attribute order, the comment, and the self-closing form must all
	// have survived byte-for-byte.
	for _, verbatim := range []string{
		`<Operation zOrder="b" aOrder="a">`,
		`<!-- a comment the platform kept -->`,
		`<Configuration enabled="false"/>`,
	} {
		if !strings.Contains(object, verbatim) {
			t.Fatalf("object lost %q:\n%s", verbatim, object)
		}
	}
}

func TestRoundTripPreservesObjectBytes(t *testing.T) {
	first, err := componentxml.DecodeBytes([]byte(sample))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	doc, err := first.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	second, err := componentxml.DecodeBytes(doc)
	if err != nil {
		t.Fatalf("re-decode of %s: %v", doc, err)
	}

	if !bytes.Equal(bytes.TrimSpace(first.ObjectXML()), bytes.TrimSpace(second.ObjectXML())) {
		t.Fatalf("object changed across the round trip:\nfirst:  %s\nsecond: %s",
			first.ObjectXML(), second.ObjectXML())
	}

	if second.ComponentID != first.ComponentID || second.Version != first.Version ||
		second.Name != first.Name || second.BranchID != first.BranchID {
		t.Fatalf("envelope drifted: %+v vs %+v", first, second)
	}
}

func TestMarshalAuthorsCreateForm(t *testing.T) {
	c := &componentxml.Component{
		Name:     "New Connection",
		Type:     "connector-settings",
		SubType:  "wss",
		FolderID: "f-9",
	}
	c.SetObjectXML([]byte(`<GenericConnectionConfig><field type="password"/></GenericConnectionConfig>`))

	doc, err := c.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	s := string(doc)

	if !strings.Contains(s, `xmlns="http://api.platform.boomi.com/"`) {
		t.Fatalf("doc lacks the platform namespace:\n%s", s)
	}

	// The only version= in the document is the XML header's own.
	if strings.Contains(s, "componentId") || strings.Count(s, "version=") != 1 {
		t.Fatalf("create form must omit componentId and version:\n%s", s)
	}

	if !strings.Contains(s, `<field type="password"/>`) {
		t.Fatalf("object subtree not carried verbatim:\n%s", s)
	}

	if !strings.HasPrefix(s, "<?xml") {
		t.Fatalf("doc lacks the XML header:\n%s", s)
	}
}

func TestEncodeRejectsAnonymousComponent(t *testing.T) {
	var c componentxml.Component

	if _, err := c.Marshal(); err == nil {
		t.Fatal("a component with neither name nor id must not marshal")
	}
}
