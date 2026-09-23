package saml

import (
	"sort"
	"strings"
)

// Canonicalization is how a signature says the bytes it covers were written
// out. A signature is a signature over canonical bytes, so reading one means
// writing the element out again exactly as the signer did.

// canonicalOptions say which canonicalization to write.
type canonicalOptions struct {
	// exclusive leaves out the namespaces an element does not use, which is
	// what a SAML assertion is signed with, because an assertion is moved
	// from one document into another.
	exclusive bool
	// prefixes are the ones an exclusive canonicalization treats as an
	// inclusive one does, which is what InclusiveNamespaces PrefixList says.
	prefixes []string
	// omit is an element left out of the output, which is how an enveloped
	// signature leaves itself out of what it covers.
	omit *Node
}

// canonical writes the element and everything under it as canonical XML.
func canonical(node *Node, options canonicalOptions) []byte {
	var out strings.Builder
	// The namespaces in scope where this element sits are the ones an
	// inclusive canonicalization carries into the output.
	scope := map[string]string{}
	if !options.exclusive {
		for element := node.Parent; element != nil; element = element.Parent {
			for _, attr := range element.Attrs {
				if prefix, ok := attr.IsNamespace(); ok {
					if _, deeper := scope[prefix]; !deeper {
						scope[prefix] = attr.Value
					}
				}
			}
		}
	}
	writeCanonicalElement(&out, node, options, map[string]string{}, scope)
	return []byte(out.String())
}

// writeCanonicalElement writes one element. rendered says which prefixes the
// output's ancestors have already declared, and inherited the declarations an
// inclusive canonicalization carries in from outside the element.
func writeCanonicalElement(out *strings.Builder, node *Node, options canonicalOptions, rendered map[string]string, inherited map[string]string) {
	if node == options.omit {
		return
	}
	declarations := canonicalNamespaces(node, options, rendered, inherited)
	out.WriteString("<" + node.Name())
	for _, declaration := range declarations {
		if declaration.prefix == "" {
			out.WriteString(` xmlns="` + escapeAttribute(declaration.value) + `"`)
			continue
		}
		out.WriteString(` xmlns:` + declaration.prefix + `="` + escapeAttribute(declaration.value) + `"`)
	}
	for _, attr := range canonicalAttributes(node) {
		out.WriteString(" " + attr.name + `="` + escapeAttribute(attr.value) + `"`)
	}
	out.WriteString(">")
	// What this element declared is what its children inherit.
	within := map[string]string{}
	for prefix, value := range rendered {
		within[prefix] = value
	}
	for _, declaration := range declarations {
		within[declaration.prefix] = declaration.value
	}
	childInherited := map[string]string{}
	for prefix, value := range inherited {
		childInherited[prefix] = value
	}
	for _, attr := range node.Attrs {
		if prefix, ok := attr.IsNamespace(); ok {
			childInherited[prefix] = attr.Value
		}
	}
	for _, item := range node.Content {
		switch value := item.(type) {
		case CharData:
			out.WriteString(escapeText(string(value)))
		case *Node:
			writeCanonicalElement(out, value, options, within, childInherited)
		}
	}
	out.WriteString("</" + node.Name() + ">")
}

// namespaceDeclaration is one xmlns written into the output.
type namespaceDeclaration struct{ prefix, value string }

// canonicalNamespaces are the namespace declarations this element writes:
// under exclusive canonicalization the ones it actually uses, under inclusive
// canonicalization everything in scope that an output ancestor has not
// already written.
func canonicalNamespaces(node *Node, options canonicalOptions, rendered, inherited map[string]string) []namespaceDeclaration {
	wanted := map[string]bool{}
	if options.exclusive {
		wanted[node.Prefix] = true
		for _, attr := range node.Attrs {
			if _, ok := attr.IsNamespace(); ok {
				continue
			}
			if attr.Prefix != "" && attr.Prefix != "xml" {
				wanted[attr.Prefix] = true
			}
		}
		for _, prefix := range options.prefixes {
			if prefix == "#default" {
				prefix = ""
			}
			wanted[prefix] = true
		}
	} else {
		for prefix := range inherited {
			wanted[prefix] = true
		}
		for _, attr := range node.Attrs {
			if prefix, ok := attr.IsNamespace(); ok {
				wanted[prefix] = true
			}
		}
		wanted[node.Prefix] = true
	}
	declarations := []namespaceDeclaration{}
	for prefix := range wanted {
		if prefix == "xml" {
			continue
		}
		value := node.lookupPrefix(prefix)
		if options.exclusive {
			if value == "" && prefix == "" {
				// A default namespace that is not there is written only to
				// undo one an output ancestor wrote.
				if rendered[""] != "" {
					declarations = append(declarations, namespaceDeclaration{prefix: "", value: ""})
				}
				continue
			}
			if value == "" {
				continue
			}
		}
		if value == "" && prefix != "" {
			continue
		}
		if existing, ok := rendered[prefix]; ok && existing == value {
			continue
		}
		if value == "" && rendered[prefix] == "" {
			continue
		}
		declarations = append(declarations, namespaceDeclaration{prefix: prefix, value: value})
	}
	sort.Slice(declarations, func(a, b int) bool { return declarations[a].prefix < declarations[b].prefix })
	return declarations
}

// canonicalAttribute is one attribute written into the output, with the name
// it is sorted by.
type canonicalAttribute struct{ name, value, namespace, local string }

// canonicalAttributes are an element's attributes without its namespace
// declarations, in the order canonical XML writes them: by the namespace they
// belong to and then by their name, with the ones that belong to none first.
func canonicalAttributes(node *Node) []canonicalAttribute {
	attributes := []canonicalAttribute{}
	for _, attr := range node.Attrs {
		if _, ok := attr.IsNamespace(); ok {
			continue
		}
		name := attr.Local
		namespace := ""
		if attr.Prefix != "" {
			name = attr.Prefix + ":" + attr.Local
			namespace = node.lookupPrefix(attr.Prefix)
		}
		attributes = append(attributes, canonicalAttribute{name: name, value: attr.Value, namespace: namespace, local: attr.Local})
	}
	sort.SliceStable(attributes, func(a, b int) bool {
		if attributes[a].namespace != attributes[b].namespace {
			return attributes[a].namespace < attributes[b].namespace
		}
		return attributes[a].local < attributes[b].local
	})
	return attributes
}

// escapeText writes text as canonical XML does.
func escapeText(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\r", "&#xD;")
	return replacer.Replace(text)
}

// escapeAttribute writes an attribute's value as canonical XML does.
func escapeAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;", "\t", "&#x9;", "\n", "&#xA;", "\r", "&#xD;")
	return replacer.Replace(value)
}
