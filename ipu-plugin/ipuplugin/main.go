// Copyright (c) 2023 Intel Corporation.  All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License")
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

package main

import (
	"context"
	"log"

	"github.com/intel/ipu-opi-plugins/ipu-plugin/pkg/ipuplugin"
	"github.com/intel/ipu-opi-plugins/ipu-plugin/pkg/p4rtclient"
	"github.com/intel/ipu-opi-plugins/ipu-plugin/pkg/types"

	pb "github.com/opiproject/opi-api/network/evpn-gw/v1alpha1/gen/go"
)

func main() {
	//cmd.Execute()

	// service := ipuplugin.NewLifeCycleService("localhost", "localhost", 9999, types.IpuMode, "/opt/p4/p4-cp-nws/bin/p4rt-ctl")

	// service.Init(context.TODO(), &pb.InitRequest{DpuMode: true})

	bridgeCtrl := ipuplugin.NewLinuxBridgeController("br0")

	if err := bridgeCtrl.EnsureBridgeExists(); err != nil {
		log.Printf("error while checking host bridge existance: %v", err)
	}

	service := ipuplugin.NewIpuPlugin(9999,
		ipuplugin.NewLinuxBridgeController("br0"),
		"/opt/p4/p4-cp-nws/bin/p4rt-ctl",
		p4rtclient.NewRHP4Client("/opt/p4/p4-cp-nws/bin/p4rt-ctl", 0x0e, "br0", types.LinuxBridge),
		"localhost", "tcp", "br0", "enp0s1f0d3", "/opt/p4/p4-cp-nws/bin", "ipu", "localhost", "localhost", 8999)

	fakeMacAddr1 := []byte{0x00, 0x14, 0x00, 0x00, 0x62, 0x15}
	fakeReq1 := &pb.CreateBridgePortRequest{
		BridgePort: &pb.BridgePort{
			Name: "fakePort1",
			Spec: &pb.BridgePortSpec{
				MacAddress:     fakeMacAddr1,
				LogicalBridges: []string{"2"},
			},
		},
	}

	service.(pb.BridgePortServiceServer).CreateBridgePort(context.TODO(), fakeReq1)

	println()

	fakeMacAddr2 := []byte{0x00, 0x13, 0x00, 0x00, 0x62, 0x15}
	fakeReq2 := &pb.CreateBridgePortRequest{
		BridgePort: &pb.BridgePort{
			Name: "fakePort2",
			Spec: &pb.BridgePortSpec{
				MacAddress:     fakeMacAddr2,
				LogicalBridges: []string{"3"},
			},
		},
	}

	service.(pb.BridgePortServiceServer).CreateBridgePort(context.TODO(), fakeReq2)
}
