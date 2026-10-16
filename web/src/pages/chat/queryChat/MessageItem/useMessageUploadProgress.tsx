import { CbEvents } from "open-im-sdk-wasm";
import { WSEvent } from "open-im-sdk-wasm/lib/types/entity";
import { useEffect, useState } from "react";

import { IMSDK } from "@/layout/MainContentWrap";

export function useMessageUploadProgress(uploadClientMsgID: string) {
  const [uploadProgress, setUploadProgress] = useState(0);

  useEffect(() => {
    const uploadHandler = ({
      data: { clientMsgID, progress },
    }: WSEvent<{ clientMsgID: string; progress: number }>) => {
      if (clientMsgID === uploadClientMsgID) {
        setUploadProgress(progress);
      }
    };
    IMSDK.on(CbEvents.OnProgress, uploadHandler);
    return () => {
      IMSDK.off(CbEvents.OnProgress, uploadHandler);
    };
  }, []);

  return uploadProgress;
}
