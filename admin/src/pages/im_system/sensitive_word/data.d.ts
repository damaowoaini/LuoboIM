export interface SensitiveWordItem {
  id: string;
  word: string;
  status: number; // 1:启用 0:禁用
  createTime: number;
  updateTime: number;
}

export interface SearchSensitiveWordParams {
  keyword?: string;
  status?: number;
  pageNumber: number;
  showNumber: number;
}

export interface AddSensitiveWordParams {
  words: string[];
}

export interface UpdateSensitiveWordParams {
  id: string;
  word?: string;
  status?: number;
}

export interface DeleteSensitiveWordParams {
  ids: string[];
}
