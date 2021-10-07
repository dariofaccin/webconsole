// SPDX-FileCopyrightText: 2021 Open Networking Foundation <info@opennetworking.org>
//
// SPDX-License-Identifier: Apache-2.0
// SPDX-License-Identifier: LicenseRef-ONF-Member-Only-1.0

/*
 * Connectivity Service Configuration
 *
 * APIs to configure connectivity service in Aether Network
 *
 * API version: 1.0.0
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package configmodels

type Slice struct {
	SliceId SliceSliceId `json:"slice-id,omitempty"`

	Qos SliceQos `json:"qos,omitempty"`

	SiteDeviceGroup []string `json:"site-device-group,omitempty"`

	SiteInfo SliceSiteInfo `json:"site-info,omitempty"`

	ApplicationFilteringRules []SliceApplicationFilteringRules `json:"application-filtering-rules,omitempty"`

	DenyApplications []string `json:"deny-applications,omitempty"`

	PermitApplications []string `json:"permit-applications,omitempty"`

	ApplicationsInformation []SliceApplicationsInformation `json:"applications-information,omitempty"`
}
