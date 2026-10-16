import { t } from "i18next";
import { memo } from "react";

const CutPopConent = ({ cutWithoutWindow }: { cutWithoutWindow?: () => void }) => {
  return (
    <div className="p-1">
      <button type="button"
        className="cursor-pointer rounded px-2 py-1 text-xs hover:bg-[var(--primary-active)]"
        onClick={cutWithoutWindow}
      >
        {t("placeholder.screenshotHideWindow")}
      </button>
    </div>
  );
};

export default memo(CutPopConent);
