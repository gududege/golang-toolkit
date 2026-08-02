package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// runXMLExport writes XML output.
func runXMLExport(cfg *Config, records []Record, groupNames []string) error {
	root := cfg.Export.XML.Root
	if root == "" {
		root = "records"
	}
	element := cfg.Export.XML.Element
	if element == "" {
		element = "record"
	}

	if !isValidXMLName(root) {
		return execErr(fmt.Sprintf("invalid XML root name %q", root))
	}
	if !isValidXMLName(element) {
		return execErr(fmt.Sprintf("invalid XML record element name %q", element))
	}

	outRecords := records
	if cfg.Export.Append {
		existing, err := parseXMLFile(cfg.Export.XML.File, root, element)
		if err != nil {
			return execErr(err.Error())
		}
		if !xmlStructureCompatible(existing, groupNames) {
			return execErr(fmt.Sprintf("XML append: structure mismatch (existing fields %v vs new %v)", existing.fields, groupNames))
		}
		outRecords = append(existing.records, records...)
	}

	if cfg.DryRun {
		out, err := marshalXML(root, element, outRecords, groupNames, cfg.Export.XML.PrettyLevel)
		if err != nil {
			return execErr(err.Error())
		}
		fmt.Println(string(out))
		return nil
	}

	out, err := marshalXML(root, element, outRecords, groupNames, cfg.Export.XML.PrettyLevel)
	if err != nil {
		return execErr(err.Error())
	}
	return writeExportFile(cfg.Export.XML.File, out)
}

var validXMLNameRe = regexp.MustCompile(`^[A-Za-z_][\w.\-]*$`)

// isValidXMLName reports whether s is a safe XML element name (simplified NCName).
func isValidXMLName(s string) bool {
	if s == "" {
		return false
	}
	return validXMLNameRe.MatchString(s)
}

// xmlFileSummary holds records parsed from an existing XML file and their field set.
type xmlFileSummary struct {
	fields  []string
	records []Record
}

// marshalXML generates XML. pretty: 0 compact, 1 one-level indent, 2 full indent.
func marshalXML(root, element string, records []Record, groupNames []string, pretty int) ([]byte, error) {
	var sb strings.Builder
	switch pretty {
	case 0:
		writeXMLCompact(&sb, root, element, records, groupNames)
	case 1:
		writeXMLOneLevel(&sb, root, element, records, groupNames)
	default:
		writeXMLFullIndent(&sb, root, element, records, groupNames, "  ")
	}
	return []byte(sb.String()), nil
}

func writeXMLCompact(sb *strings.Builder, root, element string, records []Record, groupNames []string) {
	sb.WriteString(strings.TrimSuffix(xml.Header, "\n"))
	sb.WriteByte('<')
	sb.WriteString(root)
	sb.WriteByte('>')
	for _, rec := range records {
		sb.WriteByte('<')
		sb.WriteString(element)
		sb.WriteByte('>')
		for _, g := range groupNames {
			fmt.Fprintf(sb, "<%s>%s</%s>", g, escapeXML(rec[g]), g)
		}
		sb.WriteString("</")
		sb.WriteString(element)
		sb.WriteByte('>')
	}
	sb.WriteString("</")
	sb.WriteString(root)
	sb.WriteByte('>')
}

func writeXMLOneLevel(sb *strings.Builder, root, element string, records []Record, groupNames []string) {
	sb.WriteString(strings.TrimSuffix(xml.Header, "\n"))
	sb.WriteString("\n<")
	sb.WriteString(root)
	sb.WriteString(">\n")
	for _, rec := range records {
		sb.WriteString(" <")
		sb.WriteString(element)
		sb.WriteByte('>')
		for _, g := range groupNames {
			fmt.Fprintf(sb, "<%s>%s</%s>", g, escapeXML(rec[g]), g)
		}
		sb.WriteString("</")
		sb.WriteString(element)
		sb.WriteString(">\n")
	}
	sb.WriteString("</")
	sb.WriteString(root)
	sb.WriteByte('>')
}

func writeXMLFullIndent(sb *strings.Builder, root, element string, records []Record, groupNames []string, indent string) {
	sb.WriteString(strings.TrimSuffix(xml.Header, "\n"))
	sb.WriteString("\n<")
	sb.WriteString(root)
	sb.WriteString(">\n")
	for _, rec := range records {
		sb.WriteString(indent)
		sb.WriteByte('<')
		sb.WriteString(element)
		sb.WriteString(">\n")
		for _, g := range groupNames {
			sb.WriteString(indent)
			sb.WriteString(indent)
			fmt.Fprintf(sb, "<%s>%s</%s>\n", g, escapeXML(rec[g]), g)
		}
		sb.WriteString(indent)
		sb.WriteString("</")
		sb.WriteString(element)
		sb.WriteString(">\n")
	}
	sb.WriteString("</")
	sb.WriteString(root)
	sb.WriteByte('>')
}

func escapeXML(s string) string {
	var buf strings.Builder
	xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// parseXMLFile parses an existing XML file and extracts records under the given root and element.
func parseXMLFile(path, root, element string) (*xmlFileSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &xmlFileSummary{}, nil
		}
		return nil, err
	}
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var fields []string
	seen := map[string]bool{}
	var records []Record
	var current Record
	inRecord := false
	inField := false
	var fieldName string
	var fieldValue strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XML %q: %w", path, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == element {
				inRecord = true
				current = Record{}
			} else if inRecord {
				inField = true
				fieldName = t.Name.Local
				fieldValue.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == element {
				if current != nil {
					records = append(records, current)
					current = nil
				}
				inRecord = false
			} else if inRecord && inField && t.Name.Local == fieldName {
				current[fieldName] = fieldValue.String()
				if !seen[fieldName] {
					seen[fieldName] = true
					fields = append(fields, fieldName)
				}
				inField = false
				fieldName = ""
			}
		case xml.CharData:
			if inField {
				fieldValue.WriteString(string(t))
			}
		}
	}
	return &xmlFileSummary{fields: fields, records: records}, nil
}

// xmlStructureCompatible checks whether the existing field set exactly matches new fields (order-independent).
func xmlStructureCompatible(existing *xmlFileSummary, newFields []string) bool {
	if existing == nil || len(existing.fields) == 0 {
		return true
	}
	if len(existing.fields) != len(newFields) {
		return false
	}
	es := map[string]bool{}
	for _, f := range existing.fields {
		es[f] = true
	}
	for _, f := range newFields {
		if !es[f] {
			return false
		}
	}
	return true
}
