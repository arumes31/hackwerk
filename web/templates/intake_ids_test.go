package templates

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestIntakeFormsHaveUniqueAccessibleFields(t *testing.T) {
	for _, scenario := range []struct {
		name, path string
		invalid    bool
	}{
		{name: "initial", path: "/customers/new"},
		{name: "customer and job errors", path: "/customers", invalid: true},
		{name: "customer only errors", path: "/customers/customer-only", invalid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			data := IntakeData{Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: scenario.path}}
			customerFields := []string{"first_name", "phone", "email", "notification"}
			if scenario.invalid {
				data.Error = "Please correct the customer fields"
				for _, field := range customerFields {
					data.FieldErrors = append(data.FieldErrors, FormFieldError{Field: field, Label: field, Message: "Invalid value"})
				}
			}
			var output bytes.Buffer
			if err := Intake(data).Render(t.Context(), &output); err != nil {
				t.Fatal(err)
			}
			document, err := html.Parse(&output)
			if err != nil {
				t.Fatal(err)
			}
			ids := make(map[string]*html.Node)
			var fields []*html.Node
			var summary *html.Node
			var visit func(*html.Node)
			visit = func(node *html.Node) {
				for _, attr := range node.Attr {
					if attr.Key == "id" {
						if ids[attr.Val] != nil {
							t.Errorf("duplicate ID %q", attr.Val)
						}
						ids[attr.Val] = node
					}
					if attr.Key == "data-error-summary" {
						summary = node
					}
					if attr.Key == "name" && slices.Contains(customerFields, attr.Val) {
						fields = append(fields, node)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(document)
			if len(fields) != 2*len(customerFields) {
				t.Fatalf("expected four fields in each customer form, got %d fields", len(fields))
			}
			if scenario.invalid {
				for _, field := range fields {
					var description string
					for _, attr := range field.Attr {
						if attr.Key == "aria-describedby" {
							description = attr.Val
						}
					}
					if description == "" {
						t.Fatal("invalid customer field has no error association")
					}
					for _, id := range strings.Fields(description) {
						errorNode := ids[id]
						if errorNode == nil || enclosingForm(errorNode) != enclosingForm(field) {
							t.Errorf("field error %q does not belong to its form", id)
						}
					}
				}
				if summary == nil {
					t.Fatal("missing error summary")
				}
				links := 0
				for list := summary.FirstChild; list != nil; list = list.NextSibling {
					if list.Data != "ul" {
						continue
					}
					for item := list.FirstChild; item != nil; item = item.NextSibling {
						for link := item.FirstChild; link != nil; link = link.NextSibling {
							if link.Data != "a" {
								continue
							}
							links++
							target := ids[strings.TrimPrefix(intakeAttribute(link, "href"), "#")]
							if target == nil {
								t.Errorf("summary link has no target")
								continue
							}
							if form := enclosingForm(target); form == nil || intakeAttribute(form, "action") != scenario.path {
								t.Errorf("summary target %q belongs to the wrong form", intakeAttribute(target, "id"))
							}
						}
					}
				}
				if links != len(customerFields) {
					t.Errorf("summary links = %d, want %d", links, len(customerFields))
				}
			}
		})
	}
}

func enclosingForm(node *html.Node) *html.Node {
	for node != nil {
		if node.Type == html.ElementNode && node.Data == "form" {
			return node
		}
		node = node.Parent
	}
	return nil
}

func intakeAttribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}
