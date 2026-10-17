package s3

import (
	"context"
	"time"
)

// The embedded read/signing API is unchanged. Only the actual mutators and optional MinIO thumbnail write
// add original-owner result observation; no body/error text is retained.
type ownedProvider struct {
	Interface
	transport *OwnedTransport
}

var _ Interface = (*ownedProvider)(nil)

func OwnProvider(provider Interface, transport *OwnedTransport) Interface {
	return &ownedProvider{Interface: provider, transport: transport}
}
func (p *ownedProvider) InitiateMultipartUpload(ctx context.Context, name string) (result *InitiateMultipartUploadResult, err error) {
	err = p.transport.Mutation(ctx, func(current context.Context) error {
		var e error
		result, e = p.Interface.InitiateMultipartUpload(current, name)
		return e
	})
	return
}
func (p *ownedProvider) CompleteMultipartUpload(ctx context.Context, id, name string, parts []Part) (result *CompleteMultipartUploadResult, err error) {
	err = p.transport.Mutation(ctx, func(current context.Context) error {
		var e error
		result, e = p.Interface.CompleteMultipartUpload(current, id, name, parts)
		return e
	})
	return
}
func (p *ownedProvider) DeleteObject(ctx context.Context, name string) error {
	return p.transport.Mutation(ctx, func(current context.Context) error { return p.Interface.DeleteObject(current, name) })
}
func (p *ownedProvider) CopyObject(ctx context.Context, src, dst string) (result *CopyObjectInfo, err error) {
	err = p.transport.Mutation(ctx, func(current context.Context) error {
		var e error
		result, e = p.Interface.CopyObject(current, src, dst)
		return e
	})
	return
}
func (p *ownedProvider) AbortMultipartUpload(ctx context.Context, id, name string) error {
	return p.transport.Mutation(ctx, func(current context.Context) error { return p.Interface.AbortMultipartUpload(current, id, name) })
}

// AccessURL may generate one bounded MinIO thumbnail object; pure signing/GET
// paths create no mutation attempt because observation starts at actual I/O.
func (p *ownedProvider) AccessURL(ctx context.Context, name string, expire time.Duration, opt *AccessURLOption) (result string, err error) {
	err = p.transport.Mutation(ctx, func(current context.Context) error {
		var e error
		result, e = p.Interface.AccessURL(current, name, expire, opt)
		return e
	})
	return
}
