import { useEffect } from "react";
import { message } from "@/AntdGlobalComp";
// Update metadata and installation live in the main process. Renderer-provided
// paths, ASAR archives and release URLs cannot authorize executable replacement.
export function useAutoUpdate() {
  useEffect(() => {
    if (!window.electronAPI) return;
    let cancelled = false;
    const timer = setTimeout(async () => {
      try {
        const release = await window.electronAPI!.ipcInvoke<{available:boolean;version:string}>("checkForUpdate");
        if (cancelled || !release.available) return;
        const answer = await window.electronAPI!.ipcInvoke<number>("showMessageBox",{type:"info",title:"应用更新",message:`发现萝卜对话 ${release.version}，是否下载并安装？`,buttons:["下载更新","稍后"]});
        if (cancelled || answer !== 0) return;
        message.info("正在下载更新并验证签名");
        await window.electronAPI!.ipcInvoke("downloadUpdate");
        if (!cancelled) await window.electronAPI!.ipcInvoke("quitAndUpdate");
      } catch { /* An unconfigured/unsigned development build cannot install. */ }
    },10000);
    return () => { cancelled=true;clearTimeout(timer); };
  },[]);
}
