// SPDX-FileCopyrightText: 2021 Open Networking Foundation <info@opennetworking.org>
// SPDX-FileCopyrightText: 2024 Canonical Ltd
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/omec-project/openapi/models"
	"github.com/omec-project/webconsole/backend/factory"
	"github.com/omec-project/webconsole/backend/logger"
	"github.com/omec-project/webconsole/configmodels"
	"github.com/omec-project/webconsole/dbadapter"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	authSubsDataColl = "subscriptionData.authenticationData.authenticationSubscription"
	amDataColl       = "subscriptionData.provisionedData.amData"
	smDataColl       = "subscriptionData.provisionedData.smData"
	smfSelDataColl   = "subscriptionData.provisionedData.smfSelectionSubscriptionData"
	amPolicyDataColl = "policyData.ues.amData"
	smPolicyDataColl = "policyData.ues.smData"
	flowRuleDataColl = "policyData.ues.flowRule"
	devGroupDataColl = "webconsoleData.snapshots.devGroupData"
	sliceDataColl    = "webconsoleData.snapshots.sliceData"
)

type Update5GSubscriberMsg struct {
	Msg          *configmodels.ConfigMessage
	PrevDevGroup *configmodels.DeviceGroups
	PrevSlice    *configmodels.Slice
}

var (
	execCommand        = exec.Command
	rwLock             sync.RWMutex
	subscriberAuthData SubscriberAuthenticationData
)

func configHandler(configMsgChan chan *configmodels.ConfigMessage, configReceived chan bool) {
	// Start Goroutine which will listens for subscriber config updates
	// and update the mongoDB. Only for 5G
	subsUpdateChan := make(chan *Update5GSubscriberMsg, 10)
	if factory.WebUIConfig.Configuration.Mode5G {
		go Config5GUpdateHandle(subsUpdateChan)
	}
	firstConfigRcvd := firstConfigReceived()
	if firstConfigRcvd {
		configReceived <- true
	}
	for {
		logger.ConfigLog.Infoln("waiting for configuration event")
		configMsg := <-configMsgChan
		if configMsg.MsgType == configmodels.Sub_data {
			imsiVal := strings.ReplaceAll(configMsg.Imsi, "imsi-", "")
			logger.ConfigLog.Infoln("received imsi from config channel:", imsiVal)
			if configMsg.MsgMethod == configmodels.Delete_op {
				handleSubscriberDelete(configMsg.Imsi)
			} else {
				handleSubscriberPost(configMsg.Imsi, configMsg.AuthSubData)
			}
			logger.ConfigLog.Infof("received Imsi [%v] configuration from config channel", configMsg.Imsi)
		}

		if configMsg.MsgMethod == configmodels.Post_op || configMsg.MsgMethod == configmodels.Put_op {
			if !firstConfigRcvd && (configMsg.MsgType == configmodels.Device_group || configMsg.MsgType == configmodels.Network_slice) {
				logger.ConfigLog.Debugln("first config received from ROC")
				firstConfigRcvd = true
				configReceived <- true
			}

			// update config snapshot
			if configMsg.DevGroup != nil {
				logger.ConfigLog.Infof("received Device Group [%v] configuration from config channel", configMsg.DevGroupName)
				handleDeviceGroupPost(configMsg, subsUpdateChan)
			}

			if configMsg.Slice != nil {
				logger.ConfigLog.Infof("received Slice [%v] configuration from config channel", configMsg.SliceName)
				handleNetworkSlicePost(configMsg, subsUpdateChan)
			}

			// loop through all clients and send this message to all clients
			if len(clientNFPool) == 0 {
				logger.ConfigLog.Infoln("no client available. No need to send config")
			}
			for _, client := range clientNFPool {
				logger.ConfigLog.Infoln("push config for client:", client.id)
				client.outStandingPushConfig <- configMsg
			}
		} else {
			if configMsg.MsgType != configmodels.Sub_data {
				// update config snapshot
				if configMsg.DevGroup == nil && configMsg.DevGroupName != "" {
					logger.ConfigLog.Infof("received delete Device Group [%v] from config channel", configMsg.DevGroupName)
					handleDeviceGroupDelete(configMsg, subsUpdateChan)
				}

				if configMsg.Slice == nil && configMsg.SliceName != "" {
					logger.ConfigLog.Infof("received delete Slice [%v] from config channel", configMsg.SliceName)
					handleNetworkSliceDelete(configMsg, subsUpdateChan)
				}
			} else {
				logger.ConfigLog.Infof("received delete Subscriber [%v] from config channel", configMsg.Imsi)
			}
			// loop through all clients and send this message to all clients
			if len(clientNFPool) == 0 {
				logger.ConfigLog.Infoln("no client available. No need to send config")
			}
			for _, client := range clientNFPool {
				logger.ConfigLog.Infoln("push config for client:", client.id)
				client.outStandingPushConfig <- configMsg
			}
		}
	}
}

func handleSubscriberPost(imsi string, authSubData *models.AuthenticationSubscription) {
	rwLock.Lock()
	subscriberAuthData.SubscriberAuthenticationDataCreate(imsi, authSubData)
	rwLock.Unlock()
}

func handleSubscriberDelete(imsi string) {
	rwLock.Lock()
	subscriberAuthData.SubscriberAuthenticationDataDelete(imsi)
	rwLock.Unlock()
}

func handleDeviceGroupPost(configMsg *configmodels.ConfigMessage, subsUpdateChan chan *Update5GSubscriberMsg) {
	rwLock.Lock()
	if factory.WebUIConfig.Configuration.Mode5G {
		var config5gMsg Update5GSubscriberMsg
		config5gMsg.Msg = configMsg
		config5gMsg.PrevDevGroup = getDeviceGroupByName(configMsg.DevGroupName)
		subsUpdateChan <- &config5gMsg
	}
	filter := bson.M{"group-name": configMsg.DevGroupName}
	devGroupDataBsonA := configmodels.ToBsonM(configMsg.DevGroup)
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(devGroupDataColl, filter, devGroupDataBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
	rwLock.Unlock()
}

func handleDeviceGroupDelete(configMsg *configmodels.ConfigMessage, subsUpdateChan chan *Update5GSubscriberMsg) {
	rwLock.Lock()
	if factory.WebUIConfig.Configuration.Mode5G {
		var config5gMsg Update5GSubscriberMsg
		config5gMsg.Msg = configMsg
		config5gMsg.PrevDevGroup = getDeviceGroupByName(configMsg.DevGroupName)
		subsUpdateChan <- &config5gMsg
	}
	filter := bson.M{"group-name": configMsg.DevGroupName}
	err := dbadapter.CommonDBClient.RestfulAPIDeleteOne(devGroupDataColl, filter)
	if err != nil {
		logger.DbLog.Warnln(err)
	}
	rwLock.Unlock()
}

func handleNetworkSlicePost(configMsg *configmodels.ConfigMessage, subsUpdateChan chan *Update5GSubscriberMsg) {
	rwLock.Lock()
	if factory.WebUIConfig.Configuration.Mode5G {
		var config5gMsg Update5GSubscriberMsg
		config5gMsg.Msg = configMsg
		config5gMsg.PrevSlice = getSliceByName(configMsg.SliceName)
		subsUpdateChan <- &config5gMsg
	}
	filter := bson.M{"slice-name": configMsg.SliceName}
	sliceDataBsonA := configmodels.ToBsonM(configMsg.Slice)
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(sliceDataColl, filter, sliceDataBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
	if factory.WebUIConfig.Configuration.SendPebbleNotifications {
		err := sendPebbleNotification("aetherproject.org/webconsole/networkslice/create")
		if err != nil {
			logger.ConfigLog.Warnf("sending Pebble notification failed: %s. continuing silently", err.Error())
		}
	}
	rwLock.Unlock()
}

func handleNetworkSliceDelete(configMsg *configmodels.ConfigMessage, subsUpdateChan chan *Update5GSubscriberMsg) {
	rwLock.Lock()
	if factory.WebUIConfig.Configuration.Mode5G {
		var config5gMsg Update5GSubscriberMsg
		config5gMsg.Msg = configMsg
		config5gMsg.PrevSlice = getSliceByName(configMsg.SliceName)
		subsUpdateChan <- &config5gMsg
	}
	filter := bson.M{"slice-name": configMsg.SliceName}
	err := dbadapter.CommonDBClient.RestfulAPIDeleteOne(sliceDataColl, filter)
	if err != nil {
		logger.DbLog.Warnln(err)
	}
	if factory.WebUIConfig.Configuration.SendPebbleNotifications {
		err := sendPebbleNotification("aetherproject.org/webconsole/networkslice/delete")
		if err != nil {
			logger.ConfigLog.Warnf("sending Pebble notification failed: %s. continuing silently", err.Error())
		}
	}
	rwLock.Unlock()
}

func firstConfigReceived() bool {
	return len(getDeviceGroups()) > 0 || len(getSlices()) > 0
}

func getDeviceGroups() []*configmodels.DeviceGroups {
	rawDeviceGroups, errGetMany := dbadapter.CommonDBClient.RestfulAPIGetMany(devGroupDataColl, nil)
	if errGetMany != nil {
		logger.DbLog.Warnln(errGetMany)
	}
	var deviceGroups []*configmodels.DeviceGroups
	for _, rawDevGroup := range rawDeviceGroups {
		var devGroupData configmodels.DeviceGroups
		err := json.Unmarshal(configmodels.MapToByte(rawDevGroup), &devGroupData)
		if err != nil {
			logger.DbLog.Errorf("could not unmarshall device group %v", rawDevGroup)
		}
		deviceGroups = append(deviceGroups, &devGroupData)
	}
	return deviceGroups
}

func getDeviceGroupByName(name string) *configmodels.DeviceGroups {
	filter := bson.M{"group-name": name}
	devGroupDataInterface, errGetOne := dbadapter.CommonDBClient.RestfulAPIGetOne(devGroupDataColl, filter)
	if errGetOne != nil {
		logger.DbLog.Warnln(errGetOne)
	}
	var devGroupData configmodels.DeviceGroups
	err := json.Unmarshal(configmodels.MapToByte(devGroupDataInterface), &devGroupData)
	if err != nil {
		logger.DbLog.Errorf("could not unmarshall device group %v", devGroupDataInterface)
	}
	return &devGroupData
}

func getSlices() []*configmodels.Slice {
	rawSlices, errGetMany := dbadapter.CommonDBClient.RestfulAPIGetMany(sliceDataColl, nil)
	if errGetMany != nil {
		logger.DbLog.Warnln(errGetMany)
	}
	var slices []*configmodels.Slice
	for _, rawSlice := range rawSlices {
		var sliceData configmodels.Slice
		err := json.Unmarshal(configmodels.MapToByte(rawSlice), &sliceData)
		if err != nil {
			logger.DbLog.Errorf("could not unmarshall slice %v", rawSlice)
		}
		slices = append(slices, &sliceData)
	}
	return slices
}

func getSliceByName(name string) *configmodels.Slice {
	filter := bson.M{"slice-name": name}
	sliceDataInterface, errGetOne := dbadapter.CommonDBClient.RestfulAPIGetOne(sliceDataColl, filter)
	if errGetOne != nil {
		logger.DbLog.Warnln(errGetOne)
	}
	var sliceData configmodels.Slice
	err := json.Unmarshal(configmodels.MapToByte(sliceDataInterface), &sliceData)
	if err != nil {
		logger.DbLog.Errorf("could not unmarshall slice %v", sliceDataInterface)
	}
	return &sliceData
}

func getAddedImsisList(group, prevGroup *configmodels.DeviceGroups) (aimsis []string) {
	if group == nil {
		return
	}
	for _, imsi := range group.Imsis {
		if prevGroup == nil {
			if subscriberAuthData.SubscriberAuthenticationDataGet("imsi-"+imsi) != nil {
				aimsis = append(aimsis, imsi)
			}
		} else {
			var found bool
			for _, pimsi := range prevGroup.Imsis {
				if pimsi == imsi {
					found = true
				}
			}

			if !found {
				aimsis = append(aimsis, imsi)
			}
		}
	}

	return
}

func getDeletedImsisList(group, prevGroup *configmodels.DeviceGroups) (dimsis []string) {
	if prevGroup == nil {
		return
	}

	if group == nil {
		return prevGroup.Imsis
	}

	for _, pimsi := range prevGroup.Imsis {
		var found bool
		for _, imsi := range group.Imsis {
			if pimsi == imsi {
				found = true
			}
		}

		if !found {
			dimsis = append(dimsis, pimsi)
		}
	}

	return
}

func updateAmPolicyData(imsi string) {
	// ampolicydata
	var amPolicy models.AmPolicyData
	amPolicy.SubscCats = append(amPolicy.SubscCats, "aether")
	amPolicyDatBsonA := configmodels.ToBsonM(amPolicy)
	amPolicyDatBsonA["ueId"] = "imsi-" + imsi
	filter := bson.M{"ueId": "imsi-" + imsi}
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(amPolicyDataColl, filter, amPolicyDatBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
}

func updateSmPolicyData(snssai *models.Snssai, dnn string, imsi string) {
	var smPolicyData models.SmPolicyData
	var smPolicySnssaiData models.SmPolicySnssaiData
	dnnData := map[string]models.SmPolicyDnnData{
		dnn: {
			Dnn: dnn,
		},
	}
	// smpolicydata
	smPolicySnssaiData.Snssai = snssai
	smPolicySnssaiData.SmPolicyDnnData = dnnData
	smPolicyData.SmPolicySnssaiData = make(map[string]models.SmPolicySnssaiData)
	smPolicyData.SmPolicySnssaiData[SnssaiModelsToHex(*snssai)] = smPolicySnssaiData
	smPolicyDatBsonA := configmodels.ToBsonM(smPolicyData)
	smPolicyDatBsonA["ueId"] = "imsi-" + imsi
	filter := bson.M{"ueId": "imsi-" + imsi}
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(smPolicyDataColl, filter, smPolicyDatBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
}

func updateAmProvisionedData(snssai *models.Snssai, qos *configmodels.DeviceGroupsIpDomainExpandedUeDnnQos, mcc, mnc, imsi string) {
	amData := models.AccessAndMobilitySubscriptionData{
		Gpsis: []string{
			"msisdn-0900000000",
		},
		Nssai: &models.Nssai{
			DefaultSingleNssais: []models.Snssai{*snssai},
			SingleNssais:        []models.Snssai{*snssai},
		},
		SubscribedUeAmbr: &models.AmbrRm{
			Downlink: convertToString(uint64(qos.DnnMbrDownlink)),
			Uplink:   convertToString(uint64(qos.DnnMbrUplink)),
		},
	}
	amDataBsonA := configmodels.ToBsonM(amData)
	amDataBsonA["ueId"] = "imsi-" + imsi
	amDataBsonA["servingPlmnId"] = mcc + mnc
	filter := bson.M{
		"ueId": "imsi-" + imsi,
		"$or": []bson.M{
			{"servingPlmnId": mcc + mnc},
			{"servingPlmnId": bson.M{"$exists": false}},
		},
	}
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(amDataColl, filter, amDataBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
}

func updateSmProvisionedData(snssai *models.Snssai, qos *configmodels.DeviceGroupsIpDomainExpandedUeDnnQos, mcc, mnc, dnn, imsi string) {
	// TODO smData
	smData := models.SessionManagementSubscriptionData{
		SingleNssai: snssai,
		DnnConfigurations: map[string]models.DnnConfiguration{
			dnn: {
				PduSessionTypes: &models.PduSessionTypes{
					DefaultSessionType:  models.PduSessionType_IPV4,
					AllowedSessionTypes: []models.PduSessionType{models.PduSessionType_IPV4},
				},
				SscModes: &models.SscModes{
					DefaultSscMode: models.SscMode__1,
					AllowedSscModes: []models.SscMode{
						"SSC_MODE_2",
						"SSC_MODE_3",
					},
				},
				SessionAmbr: &models.Ambr{
					Downlink: convertToString(uint64(qos.DnnMbrDownlink)),
					Uplink:   convertToString(uint64(qos.DnnMbrUplink)),
				},
				Var5gQosProfile: &models.SubscribedDefaultQos{
					Var5qi: 9,
					Arp: &models.Arp{
						PriorityLevel: 8,
					},
					PriorityLevel: 8,
				},
			},
		},
	}
	smDataBsonA := configmodels.ToBsonM(smData)
	smDataBsonA["ueId"] = "imsi-" + imsi
	smDataBsonA["servingPlmnId"] = mcc + mnc
	filter := bson.M{"ueId": "imsi-" + imsi, "servingPlmnId": mcc + mnc}
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(smDataColl, filter, smDataBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
}

func updateSmfSelectionProviosionedData(snssai *models.Snssai, mcc, mnc, dnn, imsi string) {
	smfSelData := models.SmfSelectionSubscriptionData{
		SubscribedSnssaiInfos: map[string]models.SnssaiInfo{
			SnssaiModelsToHex(*snssai): {
				DnnInfos: []models.DnnInfo{
					{
						Dnn: dnn,
					},
				},
			},
		},
	}
	smfSelecDataBsonA := configmodels.ToBsonM(smfSelData)
	smfSelecDataBsonA["ueId"] = "imsi-" + imsi
	smfSelecDataBsonA["servingPlmnId"] = mcc + mnc
	filter := bson.M{"ueId": "imsi-" + imsi, "servingPlmnId": mcc + mnc}
	_, errPost := dbadapter.CommonDBClient.RestfulAPIPost(smfSelDataColl, filter, smfSelecDataBsonA)
	if errPost != nil {
		logger.DbLog.Warnln(errPost)
	}
}

func isDeviceGroupExistInSlice(msg *Update5GSubscriberMsg) *configmodels.Slice {
	for name, slice := range getSlices() {
		for _, dgName := range slice.SiteDeviceGroup {
			if dgName == msg.Msg.DevGroupName {
				logger.WebUILog.Infof("device Group [%v] is part of slice: %v", dgName, name)
				return slice
			}
		}
	}

	return nil
}

func getAddedGroupsList(slice, prevSlice *configmodels.Slice) (names []string) {
	return getDeleteGroupsList(prevSlice, slice)
}

func getDeleteGroupsList(slice, prevSlice *configmodels.Slice) (names []string) {
	for prevSlice == nil {
		return
	}

	if slice != nil {
		for _, pdgName := range prevSlice.SiteDeviceGroup {
			var found bool
			for _, dgName := range slice.SiteDeviceGroup {
				if dgName == pdgName {
					found = true
					break
				}
			}
			if !found {
				names = append(names, pdgName)
			}
		}
	} else {
		names = append(names, prevSlice.SiteDeviceGroup...)
	}

	return
}

func removeSubscriberEntriesRelatedToDeviceGroups(mcc, mnc, imsi string) {
	filterImsiOnly := bson.M{"ueId": "imsi-" + imsi}
	filter := bson.M{"ueId": "imsi-" + imsi, "servingPlmnId": mcc + mnc}
	errDelOneAmPol := dbadapter.CommonDBClient.RestfulAPIDeleteOne(amPolicyDataColl, filterImsiOnly)
	if errDelOneAmPol != nil {
		logger.DbLog.Warnln(errDelOneAmPol)
	}
	errDelOneSmPol := dbadapter.CommonDBClient.RestfulAPIDeleteOne(smPolicyDataColl, filterImsiOnly)
	if errDelOneSmPol != nil {
		logger.DbLog.Warnln(errDelOneSmPol)
	}
	errDelOneAmData := dbadapter.CommonDBClient.RestfulAPIDeleteOne(amDataColl, filter)
	if errDelOneAmData != nil {
		logger.DbLog.Warnln(errDelOneAmData)
	}
	errDelOneSmData := dbadapter.CommonDBClient.RestfulAPIDeleteOne(smDataColl, filter)
	if errDelOneSmData != nil {
		logger.DbLog.Warnln(errDelOneSmData)
	}
	errDelOneSmfSel := dbadapter.CommonDBClient.RestfulAPIDeleteOne(smfSelDataColl, filter)
	if errDelOneSmfSel != nil {
		logger.DbLog.Warnln(errDelOneSmfSel)
	}
}

func Config5GUpdateHandle(confChan chan *Update5GSubscriberMsg) {
	for confData := range confChan {
		switch confData.Msg.MsgType {
		case configmodels.Device_group:
			rwLock.RLock()
			/* is this devicegroup part of any existing slice */
			slice := isDeviceGroupExistInSlice(confData)
			if slice != nil {
				sVal, err := strconv.ParseUint(slice.SliceId.Sst, 10, 32)
				if err != nil {
					logger.DbLog.Errorf("could not parse SST %v", slice.SliceId.Sst)
					return
				}
				snssai := &models.Snssai{
					Sd:  slice.SliceId.Sd,
					Sst: int32(sVal),
				}

				/* skip delete case */
				if confData.Msg.MsgMethod != configmodels.Delete_op {
					for _, imsi := range confData.Msg.DevGroup.Imsis {
						/* update only if the imsi is provisioned */
						if subscriberAuthData.SubscriberAuthenticationDataGet("imsi-"+imsi) != nil {
							dnn := confData.Msg.DevGroup.IpDomainExpanded.Dnn
							updateAmPolicyData(imsi)
							updateSmPolicyData(snssai, dnn, imsi)
							updateAmProvisionedData(snssai, confData.Msg.DevGroup.IpDomainExpanded.UeDnnQos, slice.SiteInfo.Plmn.Mcc, slice.SiteInfo.Plmn.Mnc, imsi)
							updateSmProvisionedData(snssai, confData.Msg.DevGroup.IpDomainExpanded.UeDnnQos, slice.SiteInfo.Plmn.Mcc, slice.SiteInfo.Plmn.Mnc, dnn, imsi)
							updateSmfSelectionProviosionedData(snssai, slice.SiteInfo.Plmn.Mcc, slice.SiteInfo.Plmn.Mnc, dnn, imsi)
						}
					}
				}

				dimsis := getDeletedImsisList(confData.Msg.DevGroup, confData.PrevDevGroup)
				for _, imsi := range dimsis {
					removeSubscriberEntriesRelatedToDeviceGroups(slice.SiteInfo.Plmn.Mcc, slice.SiteInfo.Plmn.Mnc, imsi)
				}
			}
			rwLock.RUnlock()

		case configmodels.Network_slice:
			rwLock.RLock()
			logger.WebUILog.Debugln("insert/update Network Slice")
			slice := confData.Msg.Slice
			if slice == nil && confData.PrevSlice != nil {
				logger.WebUILog.Debugln("deleted Slice:", confData.PrevSlice)
			}
			if slice != nil {
				sVal, err := strconv.ParseUint(slice.SliceId.Sst, 10, 32)
				if err != nil {
					logger.DbLog.Errorf("could not parse SST %v", slice.SliceId.Sst)
				}
				snssai := &models.Snssai{
					Sd:  slice.SliceId.Sd,
					Sst: int32(sVal),
				}
				for _, dgName := range slice.SiteDeviceGroup {
					logger.ConfigLog.Infoln("dgName:", dgName)
					devGroupConfig := getDeviceGroupByName(dgName)
					if devGroupConfig != nil {
						for _, imsi := range devGroupConfig.Imsis {
							dnn := devGroupConfig.IpDomainExpanded.Dnn
							mcc := slice.SiteInfo.Plmn.Mcc
							mnc := slice.SiteInfo.Plmn.Mnc
							updateAmPolicyData(imsi)
							updateSmPolicyData(snssai, dnn, imsi)
							updateAmProvisionedData(snssai, devGroupConfig.IpDomainExpanded.UeDnnQos, mcc, mnc, imsi)
							updateSmProvisionedData(snssai, devGroupConfig.IpDomainExpanded.UeDnnQos, mcc, mnc, dnn, imsi)
							updateSmfSelectionProviosionedData(snssai, mcc, mnc, dnn, imsi)
						}
					}
				}
			}

			dgnames := getDeleteGroupsList(slice, confData.PrevSlice)
			for _, dgname := range dgnames {
				devGroupConfig := getDeviceGroupByName(dgname)
				if devGroupConfig != nil {
					for _, imsi := range devGroupConfig.Imsis {
						removeSubscriberEntriesRelatedToDeviceGroups(confData.PrevSlice.SiteInfo.Plmn.Mcc, confData.PrevSlice.SiteInfo.Plmn.Mnc, imsi)
					}
				}
			}
			rwLock.RUnlock()
		}
	} // end of for loop
}

func convertToString(val uint64) string {
	var mbVal, gbVal, kbVal uint64
	kbVal = val / 1000
	mbVal = val / 1000000
	gbVal = val / 1000000000
	var retStr string
	if gbVal != 0 {
		retStr = strconv.FormatUint(gbVal, 10) + " Gbps"
	} else if mbVal != 0 {
		retStr = strconv.FormatUint(mbVal, 10) + " Mbps"
	} else if kbVal != 0 {
		retStr = strconv.FormatUint(kbVal, 10) + " Kbps"
	} else {
		retStr = strconv.FormatUint(val, 10) + " bps"
	}

	return retStr
}

func SnssaiModelsToHex(snssai models.Snssai) string {
	sst := fmt.Sprintf("%02x", snssai.Sst)
	return sst + snssai.Sd
}

func sendPebbleNotification(key string) error {
	cmd := execCommand("pebble", "notify", key)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("couldn't execute a pebble notify: %w", err)
	}
	logger.ConfigLog.Infoln("custom Pebble notification sent")
	return nil
}
