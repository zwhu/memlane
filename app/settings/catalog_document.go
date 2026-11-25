package settings

func documentTitle(d Document) string {
	if s := readableDocumentTitle(d); s != "" {
		return s
	}
	return d.Name
}

func documentDate(d Document) string {
	if d.Updated != "" {
		return d.Updated
	}
	return d.Created
}
