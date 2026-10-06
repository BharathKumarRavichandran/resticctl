package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode/utf16"
)

func (manager Manager) registeredWindowsTask(ctx context.Context, state State) (taskDefinition, error) {
	output, err := manager.executor.Run(ctx, nil, "schtasks", "/Query", "/TN", `\resticctl\`+nativeID(state), "/XML")
	if err != nil {
		return taskDefinition{}, fmt.Errorf("%w: %v", errDefinitionDrift, commandError("read Windows task XML", output, err))
	}
	definition, err := decodeTaskXML(output)
	if err != nil {
		return taskDefinition{}, fmt.Errorf("%w: cannot decode Windows task XML: %v", errDefinitionDrift, err)
	}
	return definition, nil
}

func (manager Manager) windowsDefinition(ctx context.Context, state State) ([]byte, error) {
	definition, err := manager.registeredWindowsTask(ctx, state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(definition.tokens)
}

func (manager Manager) verifyWindows(ctx context.Context, state State) error {
	registered, err := manager.registeredWindowsTask(ctx, state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDrift, err)
	}
	if state.RegisteredHash != "" {
		encoded, err := json.Marshal(registered.tokens)
		if err != nil {
			return err
		}
		if definitionHash(encoded) != state.RegisteredHash {
			return fmt.Errorf("%w: registered Windows task changed", ErrDrift)
		}
		return nil
	}
	// Older state hashes describe local XML. Check its explicit settings while
	// allowing Task Scheduler to add default settings during registration.
	saved, err := os.ReadFile(state.JobFile)
	if err != nil {
		return err
	}
	expected, err := decodeTaskXML(saved)
	if err != nil {
		return fmt.Errorf("%w: invalid saved Windows task XML", ErrDrift)
	}
	for path, values := range expected.fields {
		if path == "Task/Principals/Principal/UserId" && len(values) == 1 && len(registered.fields[path]) == 1 && taskUserMatches(values[0], registered.fields[path][0]) {
			continue
		}
		if !slices.Equal(values, registered.fields[path]) {
			return fmt.Errorf("%w: registered Windows task changed at %s", ErrDrift, path)
		}
	}
	return nil
}

type taskDefinition struct {
	tokens []string
	fields map[string][]string
}

// Task Scheduler exports UTF-16 XML and adds default settings at registration.
// Normalize encoding, prefixes, formatting, and registration metadata before hashing.
func decodeTaskXML(data []byte) (taskDefinition, error) {
	definition := taskDefinition{fields: make(map[string][]string)}
	if len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff) || data[0] == '<' && data[1] == 0 || data[0] == 0 && data[1] == '<') {
		if len(data)%2 != 0 {
			return taskDefinition{}, errors.New("odd UTF-16 byte count")
		}
		little := data[0] == 0xff || data[0] == '<'
		start := 0
		if data[0] == 0xff || data[0] == 0xfe {
			start = 2
		}
		units := make([]uint16, 0, len(data)/2)
		for i := start; i < len(data); i += 2 {
			unit := uint16(data[i])<<8 | uint16(data[i+1])
			if little {
				unit = uint16(data[i+1])<<8 | uint16(data[i])
			}
			units = append(units, unit)
		}
		data = []byte(string(utf16.Decode(units)))
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-16") || strings.EqualFold(charset, "utf-8") {
			return input, nil
		}
		return nil, fmt.Errorf("unsupported encoding %s", charset)
	}
	var path []string
	var content strings.Builder
	flushText := func() error {
		value := content.String()
		content.Reset()
		if strings.TrimSpace(value) == "" {
			return nil
		}
		if len(path) == 0 {
			return errors.New("text outside Task root")
		}
		definition.tokens = append(definition.tokens, "text", value)
		key := strings.Join(path, "/")
		definition.fields[key] = append(definition.fields[key], value)
		return nil
	}
	roots := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return taskDefinition{}, err
		}
		if text, ok := token.(xml.CharData); ok {
			content.Write(text)
			continue
		}
		switch token.(type) {
		case xml.StartElement, xml.EndElement:
			if err := flushText(); err != nil {
				return taskDefinition{}, err
			}
		default:
			continue
		}
		switch token := token.(type) {
		case xml.StartElement:
			if len(path) == 1 && token.Name.Local == "RegistrationInfo" {
				if err := decoder.Skip(); err != nil {
					return taskDefinition{}, err
				}
				continue
			}
			if len(path) == 0 {
				roots++
				if token.Name.Local != "Task" {
					return taskDefinition{}, errors.New("expected Task root")
				}
			}
			path = append(path, token.Name.Local)
			definition.tokens = append(definition.tokens, "start", token.Name.Local)
			var attrs []string
			for _, attr := range token.Attr {
				if attr.Name.Local == "xmlns" || attr.Name.Space == "xmlns" {
					continue
				}
				attrs = append(attrs, attr.Name.Local+"="+attr.Value)
			}
			sort.Strings(attrs)
			definition.tokens = append(definition.tokens, attrs...)
		case xml.EndElement:
			path = path[:len(path)-1]
			definition.tokens = append(definition.tokens, "end", token.Name.Local)
		}
	}
	if err := flushText(); err != nil {
		return taskDefinition{}, err
	}
	if roots != 1 || len(path) != 0 {
		return taskDefinition{}, errors.New("expected one complete Task")
	}
	return definition, nil
}
