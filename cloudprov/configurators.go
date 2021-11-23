/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	cloudprovv1alpha1 "cloudprov.org/cloudprov-controller/pkg/apis/cloudprovcontroller/v1alpha1"
)

func configurationChooserFromDriver(driverName string) func(*cloudprovv1alpha1.Postgres, map[string][]byte) DatabaseCredentials {
	switch driverName {
	case "postgresOnAzure":
		return postgresOnAzureConfigurationGenerator
	default:
		return testConfigurationGenerator
	}
}

func postgresOnAzureConfigurationGenerator(postgres *cloudprovv1alpha1.Postgres, masterConfiguration map[string][]byte) DatabaseCredentials {
	username := uuid.New().String()
	password := uuid.New().String()
	var databaseCredentials DatabaseCredentials

	if postgres.Spec.DatabaseName != "" {
		databaseCredentials.PGDATABASE_CREATE = strings.Map(normalizeRune, fmt.Sprintf("%v-%v", postgres.Namespace, postgres.Spec.DatabaseName))
	} else {
		databaseCredentials.PGDATABASE_CREATE = strings.Map(normalizeRune, "d_"+username)
	}
	databaseCredentials.PGDATABASE = databaseCredentials.PGDATABASE_CREATE
	databaseCredentials.PGHOST = strings.Map(normalizeRune, string(masterConfiguration["PGHOST"]))
	databaseCredentials.PGPASSWORD_CREATE = password
	databaseCredentials.PGPASSWORD = databaseCredentials.PGPASSWORD_CREATE
	databaseCredentials.PGPORT = string(masterConfiguration["PGPORT"])
	databaseCredentials.PGSSLMODE = string(masterConfiguration["PGSSLMODE"])
	databaseCredentials.PGUSER_CREATE = "u_" + username
	databaseCredentials.PGUSER = "u_" + username + "@" + extractServeurName(string(masterConfiguration["PGUSER"]))
	databaseCredentials.POSTGRES_SSL = string(masterConfiguration["POSTGRES_SSL"])

	return databaseCredentials
}

func testConfigurationGenerator(postgres *cloudprovv1alpha1.Postgres, masterConfiguration map[string][]byte) DatabaseCredentials {
	var databaseCredentials DatabaseCredentials
	databaseCredentials.PGUSER = "PGUSER"
	databaseCredentials.PGUSER_CREATE = "PGUSER_CREATE@TEST"
	databaseCredentials.PGPASSWORD_CREATE = "PGPASSWORD"
	databaseCredentials.PGPASSWORD = "PGPASSWORD"
	databaseCredentials.PGDATABASE = "PGDATABASE"
	databaseCredentials.PGDATABASE_CREATE = "PGDATABASE"
	return databaseCredentials
}

func extractServeurName(user string) string {
	splitted := strings.Split(string(user), "@")
	if len(splitted) > 1 {
		return splitted[1]
	} else {
		return ""
	}
}

func normalizeRune(r rune) rune {
	if strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890-.", r) {
		return r
	}
	return -1
}
