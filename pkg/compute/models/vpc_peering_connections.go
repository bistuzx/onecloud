// Copyright 2019 Yunion
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"context"
	"database/sql"
	"strings"

	"yunion.io/x/jsonutils"
	"yunion.io/x/pkg/errors"
	"yunion.io/x/pkg/util/netutils"
	"yunion.io/x/sqlchemy"

	api "yunion.io/x/onecloud/pkg/apis/compute"
	"yunion.io/x/onecloud/pkg/cloudcommon/db"
	"yunion.io/x/onecloud/pkg/cloudcommon/db/taskman"
	"yunion.io/x/onecloud/pkg/cloudprovider"
	"yunion.io/x/onecloud/pkg/httperrors"
	"yunion.io/x/onecloud/pkg/mcclient"
	"yunion.io/x/onecloud/pkg/util/stringutils2"
)

type SVpcPeeringConnectionManager struct {
	db.SEnabledStatusInfrasResourceBaseManager
	db.SExternalizedResourceBaseManager
}

var VpcPeeringConnectionManager *SVpcPeeringConnectionManager

func init() {
	VpcPeeringConnectionManager = &SVpcPeeringConnectionManager{
		SEnabledStatusInfrasResourceBaseManager: db.NewEnabledStatusInfrasResourceBaseManager(
			SVpcPeeringConnection{},
			"vpc_peering_connections_tbl",
			"vpc_peering_connection",
			"vpc_peering_connections",
		),
	}
	VpcPeeringConnectionManager.SetVirtualObject(VpcPeeringConnectionManager)
}

type SVpcPeeringConnection struct {
	db.SEnabledStatusInfrasResourceBase
	db.SExternalizedResourceBase

	SVpcResourceBase
	PeerVpcId     string `width:"36" charset:"ascii" nullable:"true" list:"domain" create:"required" json:"peer_vpc_id"`
	PeerAccountId string `width:"36" charset:"ascii" nullable:"true" list:"domain"`
	Bandwidth     int    `nullable:"false" default:"0" list:"user" create:"optional"`
}

func (manager *SVpcPeeringConnectionManager) GetContextManagers() [][]db.IModelManager {
	return [][]db.IModelManager{
		{VpcManager},
	}
}

// 列表
func (manager *SVpcPeeringConnectionManager) ListItemFilter(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	query api.VpcPeeringConnectionListInput,
) (*sqlchemy.SQuery, error) {
	var err error
	q, err = manager.SEnabledStatusInfrasResourceBaseManager.ListItemFilter(ctx, q, userCred, query.EnabledStatusInfrasResourceBaseListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SEnabledStatusInfrasResourceBaseManager.ListItemFilter")
	}

	if len(query.VpcId) > 0 {
		vpc, err := VpcManager.FetchByIdOrName(userCred, query.VpcId)
		if err != nil {
			if errors.Cause(err) == sql.ErrNoRows {
				return nil, httperrors.NewResourceNotFoundError2("vpc_id", query.VpcId)
			}
			return nil, httperrors.NewGeneralError(err)
		}
		q = q.Equals("vpc_id", vpc.GetId())
	}
	if len(query.PeerVpcId) > 0 {
		peerVpc, err := VpcManager.FetchByIdOrName(userCred, query.PeerVpcId)
		if err != nil {
			if errors.Cause(err) == sql.ErrNoRows {
				return nil, httperrors.NewResourceNotFoundError("peer_vpc_id", query.PeerVpcId)
			}
			return nil, httperrors.NewGeneralError(err)
		}
		q = q.Equals("peer_vpc_id", peerVpc.GetId())
	}
	return q, nil
}

// 创建
func (manager *SVpcPeeringConnectionManager) ValidateCreateData(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	ownerId mcclient.IIdentityProvider,
	query jsonutils.JSONObject,
	input api.VpcPeeringConnectionCreateInput,
) (api.VpcPeeringConnectionCreateInput, error) {
	var err error
	input.EnabledStatusInfrasResourceBaseCreateInput, err = manager.SEnabledStatusInfrasResourceBaseManager.ValidateCreateData(ctx, userCred, ownerId, query, input.EnabledStatusInfrasResourceBaseCreateInput)
	if err != nil {
		return input, err
	}
	if len(input.VpcId) == 0 {
		return input, httperrors.NewInputParameterError("vpc_id")
	}

	// get vpc ,peerVpc
	_vpc, err := VpcManager.FetchByIdOrName(userCred, input.VpcId)
	if err != nil {
		if errors.Cause(err) == sql.ErrNoRows {
			return input, httperrors.NewResourceNotFoundError2("vpc", input.VpcId)
		}
		return input, httperrors.NewGeneralError(err)
	}
	vpc := _vpc.(*SVpc)

	_peerVpc, err := VpcManager.FetchByIdOrName(userCred, input.PeerVpcId)
	if err != nil {
		if errors.Cause(err) == sql.ErrNoRows {
			return input, httperrors.NewResourceNotFoundError2("Peervpc", input.PeerVpcId)
		}
		return input, httperrors.NewGeneralError(err)
	}
	peerVpc := _peerVpc.(*SVpc)

	// get account,providerFactory
	account := vpc.GetCloudaccount()
	peerAccount := peerVpc.GetCloudaccount()
	if account.Provider != peerAccount.Provider {
		return input, httperrors.NewNotSupportedError("vpc on different cloudprovider peering is not supported")
	}

	factory, err := cloudprovider.GetProviderFactory(account.Provider)
	if err != nil {
		return input, httperrors.NewGeneralError(errors.Wrapf(err, "cloudprovider.GetProviderFactory(%s)", account.Provider))
	}

	// check vpc ip range overlap
	if !factory.IsSupportVpcPeeringVpcCidrOverlap() {
		vpcIpv4Ranges := []netutils.IPV4AddrRange{}
		peervpcIpv4Ranges := []netutils.IPV4AddrRange{}
		vpcCidrBlocks := strings.Split(vpc.CidrBlock, ",")
		peervpcCidrBlocks := strings.Split(peerVpc.CidrBlock, ",")
		for i := range vpcCidrBlocks {
			vpcIpv4Range, err := newIPv4RangeFromCIDR(vpcCidrBlocks[i])
			if err != nil {
				return input, httperrors.NewGeneralError(errors.Wrapf(err, "convert vpc cidr %s to ipv4range error", vpcCidrBlocks[i]))
			}
			vpcIpv4Ranges = append(vpcIpv4Ranges, vpcIpv4Range)
		}

		for i := range peervpcCidrBlocks {
			peervpcIpv4Range, err := newIPv4RangeFromCIDR(peervpcCidrBlocks[i])
			if err != nil {
				return input, httperrors.NewGeneralError(errors.Wrapf(err, "convert vpc cidr %s to ipv4range error", peervpcCidrBlocks[i]))
			}
			peervpcIpv4Ranges = append(peervpcIpv4Ranges, peervpcIpv4Range)
		}
		for i := range vpcIpv4Ranges {
			for j := range peervpcIpv4Ranges {
				if vpcIpv4Ranges[i].IsOverlap(peervpcIpv4Ranges[j]) {
					return input, httperrors.NewNotSupportedError("ipv4 range overlap")
				}
			}
		}
	}

	CrossCloudEnv := account.AccessUrl != peerAccount.AccessUrl
	CrossRegion := vpc.CloudregionId != peerVpc.CloudregionId
	if CrossCloudEnv && !factory.IsSupportCrossCloudEnvVpcPeering() {
		return input, httperrors.NewNotSupportedError("cloudprovider %s %s %s %s %s not supported CrossCloud vpcpeering", account.Provider, account.AccessUrl, vpc.CloudregionId, peerAccount.AccessUrl, peerVpc.CloudregionId)
	}
	if CrossRegion && !factory.IsSupportCrossRegionVpcPeering() {
		return input, httperrors.NewNotSupportedError("cloudprovider %s %s %s %s %s not supported CrossRegion vpcpeering", account.Provider, account.AccessUrl, vpc.CloudregionId, peerAccount.AccessUrl, peerVpc.CloudregionId)
	}
	if CrossRegion {
		err := factory.ValidateCrossRegionVpcPeeringBandWidth(input.Bandwidth)
		if err != nil {
			return input, err
		}
	}

	// existed peer check
	vpcPC := SVpcPeeringConnection{}
	err = manager.Query().Equals("vpc_id", vpc.Id).Equals("peer_vpc_id", peerVpc.Id).First(&vpcPC)
	if err == nil {
		return input, httperrors.NewNotSupportedError("vpc %s and vpc %s have already connected", input.VpcId, input.PeerVpcId)
	} else {
		if errors.Cause(err) != sql.ErrNoRows {
			return input, httperrors.NewGeneralError(err)
		}
	}

	return input, nil
}

func (self *SVpcPeeringConnection) PostCreate(ctx context.Context, userCred mcclient.TokenCredential, ownerId mcclient.IIdentityProvider, query jsonutils.JSONObject, data jsonutils.JSONObject) {
	params := jsonutils.NewDict()
	task, err := taskman.TaskManager.NewTask(ctx, "VpcPeeringConnectionCreateTask", self, userCred, params, "", "", nil)
	if err != nil {
		return
	}
	self.SetStatus(userCred, api.VPC_PEERING_CONNECTION_STATUS_CREATING, "")
	task.ScheduleRun(nil)
}

func (self *SVpcPeeringConnection) GetExtraDetails(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	query jsonutils.JSONObject,
	isList bool,
) (api.VpcPeeringConnectionDetails, error) {
	return api.VpcPeeringConnectionDetails{}, nil
}

func (manager *SVpcPeeringConnectionManager) FetchCustomizeColumns(
	ctx context.Context,
	userCred mcclient.TokenCredential,
	query jsonutils.JSONObject,
	objs []interface{},
	fields stringutils2.SSortedStrings,
	isList bool,
) []api.VpcPeeringConnectionDetails {
	rows := make([]api.VpcPeeringConnectionDetails, len(objs))
	stdRows := manager.SEnabledStatusInfrasResourceBaseManager.FetchCustomizeColumns(ctx, userCred, query, objs, fields, isList)
	vpcIds := make([]string, len(objs))
	peerVpcIds := make([]string, len(objs))
	for i := range rows {
		rows[i] = api.VpcPeeringConnectionDetails{
			EnabledStatusInfrasResourceBaseDetails: stdRows[i],
		}
		vpcPC := objs[i].(*SVpcPeeringConnection)
		vpcIds[i] = vpcPC.VpcId
		peerVpcIds[i] = vpcPC.PeerVpcId
	}

	vpcMap, err := db.FetchIdNameMap2(VpcManager, vpcIds)
	if err != nil {
		return rows
	}
	peerVpcMap, err := db.FetchIdNameMap2(VpcManager, peerVpcIds)
	if err != nil {
		return rows
	}

	for i := range rows {
		rows[i].VpcName, _ = vpcMap[vpcIds[i]]
		rows[i].PeerVpcName, _ = peerVpcMap[peerVpcIds[i]]
	}
	return rows
}

func (self *SVpcPeeringConnection) CustomizeDelete(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) error {
	return self.StartDeleteVpcPeeringConnectionTask(ctx, userCred)
}

func (self *SVpcPeeringConnection) StartDeleteVpcPeeringConnectionTask(ctx context.Context, userCred mcclient.TokenCredential) error {
	self.SetStatus(userCred, api.VPC_PEERING_CONNECTION_STATUS_DELETING, "")
	task, err := taskman.TaskManager.NewTask(ctx, "VpcPeeringConnectionDeleteTask", self, userCred, nil, "", "", nil)
	if err != nil {
		return errors.Wrap(err, "Start VpcPeeringConnectionDeleteTask fail")
	}
	task.ScheduleRun(nil)
	return nil
}

func (self *SVpcPeeringConnection) Delete(ctx context.Context, userCred mcclient.TokenCredential) error {
	return nil
}

func (self *SVpcPeeringConnection) RealDelete(ctx context.Context, userCred mcclient.TokenCredential) error {
	return self.SEnabledStatusInfrasResourceBase.Delete(ctx, userCred)
}

func (self *SVpcPeeringConnection) AllowPerformSyncstatus(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, data jsonutils.JSONObject) bool {
	return db.IsAdminAllowPerform(userCred, self, "syncstatus")
}

// 同步状态
func (self *SVpcPeeringConnection) PerformSyncstatus(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, input api.VpcSyncstatusInput) (jsonutils.JSONObject, error) {
	return nil, StartResourceSyncStatusTask(ctx, userCred, self, "VpcPeeringConnectionSyncstatusTask", "")
}

func (manager *SVpcPeeringConnectionManager) OrderByExtraFields(
	ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	query api.VpcPeeringConnectionListInput,
) (*sqlchemy.SQuery, error) {
	q, err := manager.SEnabledStatusInfrasResourceBaseManager.OrderByExtraFields(ctx, q, userCred, query.EnabledStatusInfrasResourceBaseListInput)
	if err != nil {
		return nil, errors.Wrap(err, "SEnabledStatusInfrasResourceBaseManager.OrderByExtraFields")
	}
	return q, nil
}

func (manager *SVpcPeeringConnectionManager) ListItemExportKeys(ctx context.Context,
	q *sqlchemy.SQuery,
	userCred mcclient.TokenCredential,
	keys stringutils2.SSortedStrings,
) (*sqlchemy.SQuery, error) {
	q, err := manager.SEnabledStatusInfrasResourceBaseManager.ListItemExportKeys(ctx, q, userCred, keys)
	if err != nil {
		return nil, errors.Wrap(err, "SEnabledStatusInfrasResourceBaseManager.ListItemExportKeys")
	}
	return q, nil
}

func (self *SVpcPeeringConnection) ValidateUpdateData(ctx context.Context, userCred mcclient.TokenCredential, query jsonutils.JSONObject, input api.VpcPeeringConnectionUpdateInput) (api.VpcPeeringConnectionUpdateInput, error) {
	var err error
	input.EnabledStatusInfrasResourceBaseUpdateInput, err = self.SEnabledStatusInfrasResourceBase.ValidateUpdateData(ctx, userCred, query, input.EnabledStatusInfrasResourceBaseUpdateInput)
	if err != nil {
		return input, err
	}
	return input, nil
}

func (self *SVpcPeeringConnection) syncRemove(ctx context.Context, userCred mcclient.TokenCredential) error {
	return self.RealDelete(ctx, userCred)
}

func (self *SVpcPeeringConnection) SyncWithCloudPeerConnection(ctx context.Context, userCred mcclient.TokenCredential, ext cloudprovider.ICloudVpcPeeringConnection, provider *SCloudprovider) error {
	_, err := db.Update(self, func() error {
		self.Status = ext.GetStatus()
		self.ExternalId = ext.GetGlobalId()
		self.PeerAccountId = ext.GetPeerAccountId()
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "db.Update")
	}
	if provider != nil {
		SyncCloudDomain(userCred, self, provider.GetOwnerId())
		self.SyncShareState(ctx, userCred, provider.getAccountShareInfo())
	}
	return nil
}

func (self *SVpcPeeringConnection) GetVpc() (*SVpc, error) {
	vpc, err := VpcManager.FetchById(self.VpcId)
	if err != nil {
		return nil, errors.Wrapf(err, "VpcManager.FetchById(%s)", self.VpcId)
	}
	return vpc.(*SVpc), nil
}

func (self *SVpcPeeringConnection) GetPeerVpc() (*SVpc, error) {
	vpc, err := VpcManager.FetchById(self.PeerVpcId)
	if err != nil {
		return nil, errors.Wrapf(err, "VpcManager.FetchById(%s)", self.VpcId)
	}
	return vpc.(*SVpc), nil
}
