package adminprovision

import "testing"

func TestIMOwnershipReaderRejectsDuplicateCaseUnicodeAndNamespaceAmbiguity(t *testing.T) {
	const id = "82b40ec5-561e-45d5-89ce-4ca03f955e8b"
	for _, key := range []string{publicIMMarker, adminIMMarker} {
		got, purpose, e := ReadIMMarker(`{"` + key + `":"` + id + `","custom":{"nested":[1,true]}}`)
		if e != nil || got != id || (purpose == PublicPurpose) != (key == publicIMMarker) {
			t.Fatal("valid purpose marker rejected", e)
		}
	}
	for _, ex := range []string{"", "raw legacy Ex", `{}`, `{"custom":1}`, `[]`} {
		id, _, e := ReadIMMarker(ex)
		if e != nil || id != "" {
			t.Fatal("marker-free legacy user claimed or rejected")
		}
	}
	for _, ex := range []string{`{"luoboRegistrationID":"` + id + `","luoboRegistrationID":"` + id + `"}`, `{"luoboRegistrationID":"` + id + `","luobo\u0052egistrationID":"` + id + `"}`, `{"LUOBOREGISTRATIONID":"` + id + `"}`, `{"luoboRegiſtrationID":"` + id + `"}`, `{"luoboAdminProvisioningId":"` + id + `"}`, `{"luoboRegistrationID":"` + id + `","luoboAdminProvisioningID":"` + id + `"}`, `{"luoboRegistrationID":null}`, `{"luoboRegistrationID":"not-a-uuid"}`, `{"luoboRegistrationID":"` + id + `"} {}`, `{"luoboRegistrationID":`} {
		if _, _, e := ReadIMMarker(ex); e == nil {
			t.Fatal("ambiguous/forged historical proof accepted")
		}
	}
}
