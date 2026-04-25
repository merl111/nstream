package neofs

import (
	"context"
	"errors"
	"fmt"

	"github.com/nspcc-dev/neofs-sdk-go/bearer"
	"github.com/nspcc-dev/neofs-sdk-go/client"
	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	"github.com/nspcc-dev/neofs-sdk-go/object"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"
)

// ErrFilenameNotFound is returned when no object in the container has the
// requested FileName attribute.
var ErrFilenameNotFound = errors.New("neofs: object with given FileName not found")

// ResolveFilename returns the object ID of the container object whose
// "FileName" attribute equals name. If multiple objects match (e.g. multiple
// versions), the one with the highest creation epoch is returned.
//
// Only root, physical objects are considered to avoid surfacing split-chain
// fragments as standalone files.
func (c *Client) ResolveFilename(ctx context.Context, container cid.ID, name string, token *bearer.Token) (oid.ID, error) {
	if name == "" {
		return oid.ID{}, errors.New("neofs: empty filename")
	}

	filters := object.NewSearchFilters()
	filters.AddRootFilter()
	filters.AddFilter(object.AttributeFileName, name, object.MatchStringEqual)

	var prm client.PrmObjectSearch
	prm.SetFilters(filters)
	if token != nil {
		prm.WithBearerToken(*token)
	}

	r, err := c.c.ObjectSearchInit(ctx, container, c.signer, prm)
	if err != nil {
		return oid.ID{}, fmt.Errorf("neofs: search init: %w", err)
	}
	defer r.Close()

	var (
		found    bool
		bestID   oid.ID
		bestSeen bool
	)

	if err := r.Iterate(func(id oid.ID) bool {
		if !bestSeen {
			bestID = id
			bestSeen = true
			found = true
			return false
		}
		// Tie-break by creation epoch via a HEAD lookup. We do this only when
		// more than one match exists, which is rare for typical "one file per
		// name" containers.
		bestHdr, err := c.Head(ctx, container, bestID, token)
		if err != nil {
			return false
		}
		curHdr, err := c.Head(ctx, container, id, token)
		if err != nil {
			return false
		}
		if curHdr.CreationEpoch() > bestHdr.CreationEpoch() {
			bestID = id
		}
		found = true
		return false
	}); err != nil {
		return oid.ID{}, fmt.Errorf("neofs: search iterate: %w", err)
	}

	if !found {
		return oid.ID{}, ErrFilenameNotFound
	}
	return bestID, nil
}
