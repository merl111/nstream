// Package neofs is a thin wrapper around neofs-sdk-go/client tailored to the
// streaming use case: it knows how to read object headers and arbitrary byte
// ranges from a NeoFS container, optionally with a bearer token attached.
package neofs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time" // used by CreateContainer polling

	"github.com/nspcc-dev/neofs-sdk-go/bearer"
	"github.com/nspcc-dev/neofs-sdk-go/client"
	"github.com/nspcc-dev/neofs-sdk-go/container"
	containeracl "github.com/nspcc-dev/neofs-sdk-go/container/acl"
	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	"github.com/nspcc-dev/neofs-sdk-go/netmap"
	"github.com/nspcc-dev/neofs-sdk-go/object"
	"github.com/nspcc-dev/neofs-sdk-go/object/slicer"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"
	"github.com/nspcc-dev/neofs-sdk-go/user"
)

// Config configures a Client.
type Config struct {
	// Endpoint is the NeoFS storage node URI, e.g. "grpcs://st1.t5.fs.neo.org:8080".
	Endpoint string
	// DialTimeout is the timeout for the initial gRPC dial.
	DialTimeout time.Duration
	// Signer is used to sign every outgoing NeoFS request.
	Signer user.Signer
}

// Client wraps a single connection to a NeoFS storage node.
type Client struct {
	c      *client.Client
	signer user.Signer
}

// New dials the NeoFS endpoint described by cfg and returns a ready-to-use
// Client. The caller must Close the Client when done.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, errors.New("neofs: empty endpoint")
	}
	if cfg.Signer == nil {
		return nil, errors.New("neofs: nil signer")
	}

	var prmInit client.PrmInit
	c, err := client.New(prmInit)
	if err != nil {
		return nil, fmt.Errorf("neofs: client init: %w", err)
	}

	var prmDial client.PrmDial
	prmDial.SetServerURI(cfg.Endpoint)
	if cfg.DialTimeout > 0 {
		prmDial.SetTimeout(cfg.DialTimeout)
	}
	// The SDK default is 10 s per gRPC message in a stream.  For large uploads
	// over a slow or high-latency link a single chunk can easily take longer,
	// which causes "context deadline exceeded" mid-stream.  We set an
	// effectively unlimited per-message timeout: the server process lifetime
	// (signalled via ctx) is the only real bound we want.
	prmDial.SetStreamTimeout(365 * 24 * time.Hour)
	prmDial.SetContext(ctx)

	if err := c.Dial(prmDial); err != nil {
		return nil, fmt.Errorf("neofs: dial %s: %w", cfg.Endpoint, err)
	}
	return &Client{c: c, signer: cfg.Signer}, nil
}

// Close terminates the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.c == nil {
		return nil
	}
	return c.c.Close()
}

// Head fetches the object header. It is used to learn an object's payload size,
// MIME type, file name, etc., before issuing range reads.
func (c *Client) Head(ctx context.Context, container cid.ID, id oid.ID, token *bearer.Token) (*object.Object, error) {
	var prm client.PrmObjectHead
	if token != nil {
		prm.WithBearerToken(*token)
	}
	hdr, err := c.c.ObjectHead(ctx, container, id, c.signer, prm)
	if err != nil {
		return nil, err
	}
	return hdr, nil
}

// CreateContainerOpts configures a new NeoFS container.
type CreateContainerOpts struct {
	Name      string
	Replicas  uint32 // number of object replicas; defaults to 2
	PublicRead bool   // true → acl.PublicRO, false → owner-only
}

// CreateContainerResult is returned by CreateContainer.
type CreateContainerResult struct {
	CID    cid.ID
	ACLBits uint32 // numeric value of the BasicACL that was submitted
}

// CreateContainer creates a new NeoFS container and waits for it to be
// persisted (polls ContainerGet up to ~30 s).
func (c *Client) CreateContainer(ctx context.Context, opts CreateContainerOpts) (CreateContainerResult, error) {
	if opts.Replicas == 0 {
		opts.Replicas = 2
	}

	var cnr container.Container
	cnr.Init()
	cnr.SetOwner(c.signer.UserID())

	// Placement policy: REP {n}
	var rd netmap.ReplicaDescriptor
	rd.SetNumberOfObjects(opts.Replicas)
	var policy netmap.PlacementPolicy
	policy.SetReplicas([]netmap.ReplicaDescriptor{rd})
	policy.SetContainerBackupFactor(3)
	cnr.SetPlacementPolicy(policy)

	// ACL — must be set explicitly; zero value (0x0) denies everything.
	var acl containeracl.Basic
	if opts.PublicRead {
		acl = containeracl.PublicRO
	} else {
		acl = containeracl.Private
	}
	cnr.SetBasicACL(acl)

	if opts.Name != "" {
		cnr.SetName(opts.Name)
	}
	cnr.SetCreationTime(time.Now())

	var prm client.PrmContainerPut
	newCID, err := c.c.ContainerPut(ctx, cnr, c.signer, prm)
	if err != nil {
		return CreateContainerResult{}, fmt.Errorf("container put: %w", err)
	}

	res := CreateContainerResult{CID: newCID, ACLBits: acl.Bits()}

	// Poll until the container appears on the network (eventual consistency).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := c.c.ContainerGet(ctx, newCID, client.PrmContainerGet{})
		if err == nil {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	// Return the CID even if polling timed out — it may still be propagating.
	return res, nil
}

// Put uploads an object with the given filename and content-type.
// It uses the SDK slicer to automatically split payloads that exceed the
// network's maximum object size (fetched via NetworkInfo on every call).
// Returns the root object ID.
func (c *Client) Put(ctx context.Context, cnr cid.ID, filename, contentType string, r io.Reader) (oid.ID, error) {
	var attrs []object.Attribute
	if filename != "" {
		attrs = append(attrs, object.NewAttribute(object.AttributeFileName, filename))
	}
	if contentType != "" {
		attrs = append(attrs, object.NewAttribute(object.AttributeContentType, contentType))
	}

	s, err := slicer.New(ctx, c.c, c.signer, cnr, c.signer.UserID(), nil)
	if err != nil {
		return oid.ID{}, fmt.Errorf("slicer init: %w", err)
	}

	id, err := s.Put(ctx, r, attrs)
	if err != nil {
		return oid.ID{}, fmt.Errorf("slicer put: %w", err)
	}
	return id, nil
}

// Search returns all object IDs in the container matching the given filters.
func (c *Client) Search(ctx context.Context, container cid.ID, filters object.SearchFilters, token *bearer.Token) ([]oid.ID, string, error) {
	var opts client.SearchObjectsOptions
	if token != nil {
		opts.WithBearerToken(*token)
	}
	results, cursor, err := c.c.SearchObjects(ctx, container, filters, nil, "", c.signer, opts)
	if err != nil {
		return nil, "", err
	}
	ids := make([]oid.ID, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids, cursor, nil
}

// GetWithHeader opens a streaming reader for the entire object payload and
// returns the object header alongside it. Unlike Head (which uses ObjectHead),
// this uses ObjectGetInit and therefore correctly returns the *parent* header
// for complex/split objects — including the true total PayloadSize.
//
// The caller must Close the returned reader when done.
// Cancelling ctx tears the stream down.
func (c *Client) GetWithHeader(ctx context.Context, container cid.ID, id oid.ID, token *bearer.Token) (*object.Object, io.ReadCloser, error) {
	var prm client.PrmObjectGet
	if token != nil {
		prm.WithBearerToken(*token)
	}
	hdr, r, err := c.c.ObjectGetInit(ctx, container, id, c.signer, prm)
	if err != nil {
		return nil, nil, err
	}
	return &hdr, r, nil
}

// Get opens a streaming reader for the entire object payload.
//
// The returned reader holds an open gRPC stream; the caller must Close it.
// Cancelling ctx tears the stream down.
func (c *Client) Get(ctx context.Context, container cid.ID, id oid.ID, token *bearer.Token) (io.ReadCloser, error) {
	_, r, err := c.GetWithHeader(ctx, container, id, token)
	return r, err
}

// Range opens a streaming reader over the byte range [offset, offset+length)
// of the object. Both offset and length must be > 0; for the full object use Get.
//
// The returned reader holds an open gRPC stream; the caller must Close it.
// Cancelling ctx (e.g. on HTTP client disconnect) tears the stream down.
func (c *Client) Range(ctx context.Context, container cid.ID, id oid.ID, offset, length uint64, token *bearer.Token) (io.ReadCloser, error) {
	var prm client.PrmObjectRange
	if token != nil {
		prm.WithBearerToken(*token)
	}
	r, err := c.c.ObjectRangeInit(ctx, container, id, offset, length, c.signer, prm)
	if err != nil {
		return nil, err
	}
	return r, nil
}
