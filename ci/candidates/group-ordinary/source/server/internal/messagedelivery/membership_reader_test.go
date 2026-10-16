package messagedelivery

import "testing"

func TestHistoryWindowEnvironmentBindingNeverLazilyEnablesPartialSettings(t *testing.T) {
	for _, name:=range []string{HistoryWindowModeEnv,HistoryWindowEpochEnv,HistoryWindowActivationEnv} { t.Setenv(name,"") }
	if r,err:=NewHistoryWindowReaderFromEnvironment(nil);err!=nil || r!=nil { t.Fatal("legacy mode constructed a reader",err) }
	if err:=CheckLegacyMsgTransferHistoryMode();err!=nil { t.Fatal("legacy transfer mode rejected",err) }
	for _, pair:=range [][2]string{{HistoryWindowModeEnv,"1"},{HistoryWindowEpochEnv,"00000000-0000-4000-8000-000000000001"},{HistoryWindowActivationEnv,"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{HistoryWindowModeEnv,"required"}} {
		t.Setenv(pair[0],pair[1])
		if err:=CheckLegacyMsgTransferHistoryMode();err==nil { t.Fatal("half-on migration left the actual old allocator eligible to start") }
		if _,err:=NewHistoryWindowReaderFromEnvironment(nil);err==nil { t.Fatal("partial settings or missing Mongo silently activated reader") }
		t.Setenv(pair[0],"")
	}
}

func TestHistoryWindowCIDDoesNotAliasNotificationOrMalformedGroup(t *testing.T) {
	for _, cid:=range []string{"n_g","si_g","sg_"," sg_g","sg_g\x00x"} { if groupWindowCID(cid) { t.Fatal("noncanonical group CID accepted") } }
	if !groupWindowCID("sg_g") { t.Fatal("normal group CID rejected") }
}
