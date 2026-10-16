import { Avatar as AntdAvatar, AvatarProps } from "antd";
import clsx from "clsx";
import * as React from "react";
import { useMemo } from "react";

// import default_group from "@/assets/images/contact/group.png";
import default_group from "@/assets/images/contact/my_groups.png";
import { useMessageStore, useUserStore } from "@/store";
import { avatarList, getDefaultAvatar } from "@/utils/avatar";
import { downloadFile } from "@/utils/common";
import { mobileDesign } from "@/design/tokens";

const default_avatars = avatarList.map((item) => item.name);

interface IOIMAvatarProps extends AvatarProps {
  text?: string;
  color?: string;
  bgColor?: string;
  isgroup?: boolean;
  isnotification?: boolean;
  size?: number;
}

const OIMAvatar: React.FC<IOIMAvatarProps> = (props) => {
  const {
    src,
    text,
    size = 42,
    color = mobileDesign.text,
    bgColor = mobileDesign.primarySoft,
    isgroup = false,
    isnotification,
  } = props;
  const [errorHolder, setErrorHolder] = React.useState<string>();

  const getAvatarUrl = useMemo(() => {
    if (src) {
      if (default_avatars.includes(src as string))
        return getDefaultAvatar(src as string);

      if (!window.electronAPI) return src;

      const cachePath = useUserStore.getState().imageCache[src as string];
      if (cachePath && window.electronAPI?.fileExists(cachePath)) {
        return `file://${cachePath}`;
      }
      const hasTask =
        Object.values(useMessageStore.getState().downloadMap).findIndex(
          (task) => task.originUrl === src,
        ) > -1;
      if (!hasTask) {
        downloadFile(src as string, {
          isThumb: true,
          saveType: "avatar",
        });
      }

      return src;
    }
    return isgroup ? default_group : undefined;
  }, [src, isgroup, isnotification]);

  const avatarProps = { ...props, isgroup: undefined, isnotification: undefined };
  const initials = useMemo(() => {
    const value = text?.trim() || "?";
    const segments = Array.from(new Intl.Segmenter(undefined, { granularity: "grapheme" }).segment(value), item => item.segment);
    return segments.length > 2 ? segments.slice(-2).join("") : value;
  }, [text]);

  React.useEffect(() => {
    if (!isgroup) {
      setErrorHolder(undefined);
    }
  }, [isgroup]);

  const errorHandler = () => {
    if (isgroup) {
      setErrorHolder(default_group);
    }
  };

  return (
    <AntdAvatar
      style={{
        backgroundColor: bgColor,
        borderRadius: mobileDesign.radiusControl,
        minWidth: `${size}px`,
        minHeight: `${size}px`,
        lineHeight: `${size - 2}px`,
        color,
      }}
      shape="square"
      aria-label={text}
      {...avatarProps}
      className={clsx(
        {
          "cursor-pointer": Boolean(props.onClick),
        },
        props.className,
      )}
      src={errorHolder ?? getAvatarUrl}
      onError={errorHandler as any}
    >
      {initials}
    </AntdAvatar>
  );
};

export default OIMAvatar;
