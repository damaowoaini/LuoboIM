import { t } from "i18next";
import { memo } from "react";

import publish_image from "@/assets/images/moments/publish_image.png";
import publish_video from "@/assets/images/moments/publish_video.png";

const PublishMenu = memo(
  ({ preparePublish }: { preparePublish: (withVideo?: boolean) => void }) => {
    return (
      <div className="app-no-drag px-2 py-1">
        <button type="button"
          className="flex w-full cursor-pointer items-center border-b border-b-gray-200 p-2 text-left hover:bg-[var(--primary-active)]"
          onClick={() => preparePublish()}
        >
          <img src={publish_image} width={14} alt="" />
          <span className="ml-1.5">{t("placeholder.publishImages")}</span>
        </button>
        <button type="button"
          className="flex w-full cursor-pointer items-center p-2 text-left hover:bg-[var(--primary-active)]"
          onClick={() => preparePublish(true)}
        >
          <img src={publish_video} width={14} alt="" />
          <span className="ml-1.5">{t("placeholder.publishVideo")}</span>
        </button>
      </div>
    );
  },
);

export default PublishMenu;
