export interface ComplaintItem {
  id: string;
  complaintUserID: string;
  complaintType: number; // 1=用户 2=群组
  targetID: string;
  targetName: string;
  reason: string;
  description: string;
  evidence: string;
  status: number; // 0=待处理 1=已处理 2=已驳回
  handlerID: string;
  handleResult: string;
  createTime: string;
  updateTime: string;
  handleTime: string | null;
}

export interface SearchComplaintParams {
  complaintUserID?: string;
  targetID?: string;
  complaintType?: number;
  status?: number;
  pageNumber: number;
  showNumber: number;
}

export interface HandleComplaintParams {
  id: string;
  status: number;
  handlerID: string;
  handleResult: string;
}
