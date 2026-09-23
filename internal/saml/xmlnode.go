// Package saml reads SAML 2.0 single sign-on: the assertion an identity
// provider posts back, the request that asks for one, and the metadata that
// describes this site to the provider.
//
// A SAML assertion is trusted only because of the signature over it, so the
// XML it arrives as is read the way the signature was computed: prefixes and
// all. Go's own decoder resolves namespaces and throws the prefixes away,
// which is enough to read a document and not enough to canonicalize one, so
// this package keeps its own small tree.
package saml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Node is one element, with what it was written with: the prefix, the
// attributes in document order, and its content in document order.
type Node struct {
	Prefix, Local string
	Attrs         []Attr
	Content       []Content
	Parent        *Node
}

// Attr is one attribute as it was written, including a namespace
// declaration, which is written as xmlns or xmlns:prefix.
type Attr struct {
	Prefix, Local, Value string
}

// Content is either an element or the text between elements.
type Content interface{ content() }

// CharData is the text between elements.
type CharData string

func (*Node) content()    {}
func (CharData) content() {}

// Name is the qualified name as it was written.
func (n *Node) Name() string {
	if n.Prefix == "" {
		return n.Local
	}
	return n.Prefix + ":" + n.Local
}

// IsNamespace reports whether the attribute declares a namespace, and which
// prefix it declares; the default namespace declares the empty prefix.
func (a Attr) IsNamespace() (string, bool) {
	switch {
	case a.Prefix == "xmlns":
		return a.Local, true
	case a.Prefix == "" && a.Local == "xmlns":
		return "", true
	}
	return "", false
}

// Parse reads a document into a tree that remembers how it was written. A
// document type declaration is refused: an assertion never carries one, and
// an entity in one is a way into the files of whoever reads it.
func Parse(document []byte) (*Node, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(document)))
	decoder.Strict = true
	var root, current *Node
	for {
		token, err := decoder.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the document: %w", err)
		}
		switch value := token.(type) {
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(value)), "DOCTYPE") {
				return nil, errors.New("a document type declaration is not read here")
			}
		case xml.StartElement:
			node := &Node{Prefix: value.Name.Space, Local: value.Name.Local, Parent: current}
			for _, attr := range value.Attr {
				node.Attrs = append(node.Attrs, Attr{Prefix: attr.Name.Space, Local: attr.Name.Local, Value: attr.Value})
			}
			if current == nil {
				if root != nil {
					return nil, errors.New("a document holds one element")
				}
				root = node
			} else {
				current.Content = append(current.Content, node)
			}
			current = node
		case xml.EndElement:
			if current == nil {
				return nil, errors.New("an element ends where none began")
			}
			if current.Local != value.Name.Local || current.Prefix != value.Name.Space {
				return nil, fmt.Errorf("%s ends as %s", current.Name(), value.Name.Local)
			}
			current = current.Parent
		case xml.CharData:
			if current != nil {
				current.Content = append(current.Content, CharData(value))
			}
		}
	}
	if root == nil {
		return nil, errors.New("the document holds no element")
	}
	if current != nil {
		return nil, errors.New("an element never ends")
	}
	return root, nil
}

// Children are the elements directly under this one.
func (n *Node) Children() []*Node {
	children := []*Node{}
	for _, item := range n.Content {
		if child, ok := item.(*Node); ok {
			children = append(children, child)
		}
	}
	return children
}

// Child is the first element under this one with the given namespace and
// local name, or nil.
func (n *Node) Child(namespace, local string) *Node {
	for _, child := range n.Children() {
		if child.Local == local && child.Namespace() == namespace {
			return child
		}
	}
	return nil
}

// ChildrenNamed are every element directly under this one with the given
// namespace and local name.
func (n *Node) ChildrenNamed(namespace, local string) []*Node {
	found := []*Node{}
	for _, child := range n.Children() {
		if child.Local == local && child.Namespace() == namespace {
			found = append(found, child)
		}
	}
	return found
}

// Namespace is the namespace the element's own prefix is bound to where it
// sits.
func (n *Node) Namespace() string { return n.lookupPrefix(n.Prefix) }

// lookupPrefix answers what a prefix is bound to at this element, walking up
// through the declarations its ancestors made.
func (n *Node) lookupPrefix(prefix string) string {
	if prefix == "xml" {
		return xmlNamespace
	}
	for element := n; element != nil; element = element.Parent {
		for _, attr := range element.Attrs {
			if declared, ok := attr.IsNamespace(); ok && declared == prefix {
				return attr.Value
			}
		}
	}
	return ""
}

// Attr is the value of an attribute of this element with no prefix, and
// whether it is there at all.
func (n *Node) Attr(local string) (string, bool) {
	for _, attr := range n.Attrs {
		if attr.Prefix == "" && attr.Local == local {
			return attr.Value, true
		}
	}
	return "", false
}

// Text is the text directly inside this element, with the text of everything
// under it, which is what a SAML value is.
func (n *Node) Text() string {
	var text strings.Builder
	var walk func(node *Node)
	walk = func(node *Node) {
		for _, item := range node.Content {
			switch value := item.(type) {
			case CharData:
				text.WriteString(string(value))
			case *Node:
				walk(value)
			}
		}
	}
	walk(n)
	return text.String()
}

// elementByID finds the element an ID attribute names, which is how a
// signature says what it covers. An id that names two elements is refused:
// which one a signature covers must not be a matter of opinion.
func (n *Node) elementByID(id string) (*Node, error) {
	var found *Node
	var walk func(node *Node) error
	walk = func(node *Node) error {
		for _, attr := range node.Attrs {
			if attr.Prefix != "" || !strings.EqualFold(attr.Local, "ID") {
				continue
			}
			if attr.Value == id {
				if found != nil {
					return fmt.Errorf("two elements are called %q", id)
				}
				found = node
			}
		}
		for _, child := range node.Children() {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(n); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("nothing in the document is called %q", id)
	}
	return found, nil
}

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"
