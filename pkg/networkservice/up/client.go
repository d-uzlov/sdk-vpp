// Copyright (c) 2020-2022 Cisco and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package up provides chain elements to 'up' interfaces (and optionally wait for them to come up)
package up

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/api/pkg/api/networkservice"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/chain"
	"github.com/networkservicemesh/sdk/pkg/networkservice/core/next"
	"github.com/networkservicemesh/sdk/pkg/networkservice/utils/metadata"
	"github.com/networkservicemesh/sdk/pkg/tools/postpone"

	"github.com/networkservicemesh/sdk-vpp/pkg/networkservice/up/ipsecup"
	"github.com/networkservicemesh/sdk-vpp/pkg/networkservice/up/peerup"
	"github.com/networkservicemesh/sdk-vpp/pkg/tools/ifindex"
)

type upClient struct {
	ctx         context.Context
	vppConn     Connection
	loadIfIndex ifIndexFunc

	inited    uint32
	initMutex sync.Mutex
}

// NewClient provides a NetworkServiceClient chain elements that 'up's the swIfIndex
func NewClient(ctx context.Context, vppConn Connection, opts ...Option) networkservice.NetworkServiceClient {
	o := &options{
		loadIfIndex: ifindex.Load,
	}
	for _, opt := range opts {
		opt(o)
	}

	return chain.NewNetworkServiceClient(
		peerup.NewClient(ctx, vppConn),
		&upClient{
			ctx:         ctx,
			vppConn:     vppConn,
			loadIfIndex: o.loadIfIndex,
		},
		ipsecup.NewClient(ctx, vppConn),
	)
}

func (u *upClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*networkservice.Connection, error) {
	if err := u.init(ctx); err != nil {
		return nil, err
	}

	postponeCtxFunc := postpone.ContextWithValues(ctx)

	conn, err := next.Client(ctx).Request(ctx, request, opts...)
	if err != nil {
		return nil, err
	}

	if err := up(ctx, u.vppConn, u.loadIfIndex, metadata.IsClient(u)); err != nil {
		closeCtx, cancelClose := postponeCtxFunc()
		defer cancelClose()

		if _, closeErr := u.Close(closeCtx, conn, opts...); closeErr != nil {
			err = errors.Wrapf(err, "connection closed with error: %s", closeErr.Error())
		}

		return nil, err
	}

	return conn, nil
}

func (u *upClient) Close(ctx context.Context, conn *networkservice.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	return next.Client(ctx).Close(ctx, conn, opts...)
}

func (u *upClient) init(ctx context.Context) error {
	if atomic.LoadUint32(&u.inited) > 0 {
		return nil
	}
	u.initMutex.Lock()
	defer u.initMutex.Unlock()
	if atomic.LoadUint32(&u.inited) > 0 {
		return nil
	}

	err := initFunc(ctx, u.vppConn)
	if err == nil {
		atomic.StoreUint32(&u.inited, 1)
	}
	return err
}
