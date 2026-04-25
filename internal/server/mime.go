package server

import (
	"mime"
	"path"
	"strings"

	"github.com/nspcc-dev/neofs-sdk-go/object"
)

// detectContentType picks a Content-Type for the response, in priority order:
//
//  1. The "Content-Type" attribute on the NeoFS object header.
//  2. mime.TypeByExtension on the URL path's extension or the FileName attr.
//  3. "application/octet-stream" (lets clients sniff if they wish).
func detectContentType(hdr *object.Object, urlPath string) string {
	if hdr != nil {
		var fileName string
		for _, a := range hdr.Attributes() {
			switch a.Key() {
			case object.AttributeContentType:
				if v := strings.TrimSpace(a.Value()); v != "" {
					return v
				}
			case object.AttributeFileName:
				fileName = a.Value()
			}
		}
		if fileName != "" {
			if ct := mime.TypeByExtension(path.Ext(fileName)); ct != "" {
				return ct
			}
		}
	}
	if urlPath != "" {
		if ct := mime.TypeByExtension(path.Ext(urlPath)); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

// fileNameAttr returns the FileName attribute value if set, else "".
func fileNameAttr(hdr *object.Object) string {
	if hdr == nil {
		return ""
	}
	for _, a := range hdr.Attributes() {
		if a.Key() == object.AttributeFileName {
			return a.Value()
		}
	}
	return ""
}
