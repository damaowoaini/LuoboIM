// Unsigned ASAR extraction and sudo shell execution are intentionally rejected.
// Signed installer updates must be implemented and verified before release.
export const asarHotUpdate = async (_pkgPath: string): Promise<boolean> => {
  throw new Error('仅支持正式签名安装包更新');
};
