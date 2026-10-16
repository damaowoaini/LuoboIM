import md5 from 'md5';

export interface AdminRegistrationInput {
  nickname: string;
  gender?: number;
  birth?: number;
  phoneNumber: string;
  areaCode: string;
  password: string;
}

export function adminRegistrationBody(input: AdminRegistrationInput, faceURL = '') {
  return {
    platform: 5,
    autoLogin: false,
    deviceID: '',
    user: {
      nickname: input.nickname.trim(),
      gender: input.gender === 1 || input.gender === 2 ? input.gender : 0,
      birth: input.birth ?? 0,
      areaCode: `+${input.areaCode.replace(/^\+/, '')}`,
      phoneNumber: input.phoneNumber.trim(),
      password: md5(input.password),
      faceURL,
    },
  };
}

/** Online detail deliberately has no credential/token field. */
export interface AdminOnlineDetail {
  userID: string;
  status?: string;
  singlePlatformToken?: { platform: string; total?: number }[];
}

export function onlineDeviceLabel(details: AdminOnlineDetail['singlePlatformToken']): string {
  const platforms = [...new Set((details ?? []).map(detail => detail.platform).filter(Boolean))];
  return platforms.length ? `${platforms.join('/')}在线` : '离线';
}

type EditableUserProfile = {
  nickname?: string;
  gender?: number;
  birth?: number;
  level?: number;
};

/** Table rows also contain identity, security and presentation fields. Never spread them into a mutation. */
export function changedUserProfile(userID: string, edited: EditableUserProfile, original: EditableUserProfile) {
  const changes: EditableUserProfile & { userID: string } = { userID };
  for (const field of ['nickname', 'gender', 'birth', 'level'] as const) {
    const next = edited[field];
    if (next !== undefined && next !== original[field]) Object.assign(changes, { [field]: next });
  }
  return changes;
}

export function formatRecordDuration(start: number, end: number): string {
  if (!Number.isFinite(start) || start <= 0 || !Number.isFinite(end)) return '—';
  if (end === 0) return '未结束';
  if (end < start) return '—';
  const seconds = Math.floor(end - start);
  return [Math.floor(seconds / 3600), Math.floor(seconds / 60) % 60, seconds % 60]
    .map(value => String(value).padStart(2, '0')).join(':');
}
