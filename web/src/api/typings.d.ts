declare namespace API {
  declare namespace Login {
    enum UsedFor {
      Register = 1,
      Modify = 2,
      Login = 3,
    }
    type RegisterUserInfo = {
      nickname: string;
      faceURL: string;
      birth?: number;
      gender?: number;
      email?: string;
      account?: string;
      areaCode: string;
      phoneNumber?: string;
      password: string;
    };
    type DemoRegisterType = {
      invitationCode?: string;
      verifyCode: string;
      deviceID?: string;
      autoLogin?: boolean;
      user: RegisterUserInfo;
    };
    type LoginParams = {
      email?: string;
      verifyCode?: string;
      deviceID?: string;
      phoneNumber?: string;
      areaCode: string;
      account?: string;
      password?: string;
      googleAuthCode?: string;
    };
    type ModifyParams = {
      userID: string;
      currentPassword: string;
      newPassword: string;
    };
    type ResetParams = {
      email?: string;
      phoneNumber?: string;
      areaCode: string;
      verifyCode: string;
      password: string;
    };
    type VerifyCodeParams = {
      invitationCode?: string;
      email?: string;
      phoneNumber?: string;
      areaCode: string;
      verifyCode: string;
      usedFor: UsedFor;
    };
    type SendSmsParams = {
      email?: string;
      phoneNumber?: string;
      areaCode: string;
      deviceID?: string;
      usedFor: UsedFor;
      invitationCode?: string;
    };
  }

  declare namespace User {
    // 用户登录记录
    type UserLoginRecord = {
      userID: string;
      loginTime: number;
      ip: string;
      deviceID: string;
      platform: string;
      deviceModel: string;
      location: string;
      isMultiDevice: boolean;  // 是否同一设备多账户登录
      otherUserIDs: string[];  // 同一设备登录的其他账户ID
    };

    // 获取最新登录记录参数
    type GetLatestLoginRecordParams = {
      userID: string;
    };

    // 获取登录记录列表参数
    type GetLoginRecordsParams = {
      userID: string;
      pagination: {
        pageNumber: number;
        showNumber: number;
      };
    };

    // 获取最新登录记录响应
    type LatestLoginRecordResponse = {
      record: UserLoginRecord;
    };

    // 获取登录记录列表响应
    type LoginRecordsResponse = {
      total: number;
      records: UserLoginRecord[];
    };
  }
  declare namespace Moments {
    type User = {
      userID: string;
      userName: string;
    };
    type PublishMomentsParams = {
      content: {
        metas: {
          original: string;
          thumb: string;
        }[];
        text: string;
        type: number;
      };
      permission: 0 | 1 | 2 | 3;
      atUserIDs?: string[];
      permissionUserIDs?: string[];
      permissionGroupIDs?: string[];
    };
    type DeleteCommentParams = {
      workMomentID: string;
      commentID: string;
    };
    type CreateCommentParams = {
      workMomentID: string;
      content: string;
      replyUserID: string;
    };
  }
}
