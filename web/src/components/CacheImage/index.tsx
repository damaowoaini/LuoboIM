import { Image, ImageProps } from "antd";
import { useMemo } from "react";

import { useUserStore } from "@/store";
import { downloadFile } from "@/utils/common";
import { devObjectTransportURL } from "@/core/session/devObjectTransport";

const CacheImage = ({ cache = true, ...props }: ImageProps & { cache?: boolean }) => {
  const getSourceUrl = useMemo(() => {
    if (!cache || !window.electronAPI || props.src?.match(/^blob:/)) return props.src;

    if (!props.src?.match(/^https?:\/\//) && !props.src?.match(/^file:\/\//)) {
      return `file://${props.src}`;
    }
    const cachePath = useUserStore.getState().imageCache[props.src];
    if (cachePath && window.electronAPI?.fileExists(cachePath)) {
      return `file://${cachePath}`;
    }
    if (props.src) {
      downloadFile(props.src, {
        isThumb: true,
        saveType: "image",
        randomName: true,
      });
    }
    return props.src;
  }, [props.src, cache]);
  return <Image {...props} src={!window.electronAPI && getSourceUrl ? devObjectTransportURL(getSourceUrl) : getSourceUrl} />;
};

export default CacheImage;
