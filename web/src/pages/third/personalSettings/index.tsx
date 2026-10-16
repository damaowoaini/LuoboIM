import { openBlackList, openChangePassword } from "@/utils/childWindows";
import { PersonalSettingsContent } from "@/layout/LeftNavBar/PersonalSettings";

export const PersonalSettings = () => {
  const closeWindow = () => {
    window.electronAPI?.ipcInvoke("closeWindow", "personal-settings");
  };
  return (
    <div className="h-full bg-[var(--chat-bubble)]">
      <PersonalSettingsContent
        closeOverlay={closeWindow}
        openBackListOverlay={openBlackList}
        openChangePasswordOverlay={openChangePassword}
      />
    </div>
  );
};
