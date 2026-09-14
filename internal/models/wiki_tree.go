package models

// WikiContentTypeName names a kind of content-tree node the way the product
// shows it in a sentence: "page", "folder", "whiteboard", "database" and
// "Smart Link".
func WikiContentTypeName(contentType string) string {
	switch contentType {
	case "embed":
		return "Smart Link"
	case "":
		return ""
	}
	return contentType
}

// ParentTypeName names the kind of this content's parent.
func (c WikiContent) ParentTypeName() string { return WikiContentTypeName(c.ParentType) }
