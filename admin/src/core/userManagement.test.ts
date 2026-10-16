import { describe, expect, it } from 'vitest';
import { adminRegistrationBody, changedUserProfile, formatRecordDuration, onlineDeviceLabel } from './userManagement';

describe('administrator user-management contracts', () => {
  it('sends a nested MD5 credential without echoing the plaintext form or a universal OTP', () => {
    const birth = Date.UTC(1997, 4, 6);
    const result = adminRegistrationBody({ nickname:' 合成用户 ', gender:2, birth, areaCode:'+86',
      phoneNumber:' 19900000000 ', password:'synthetic-password' });
    expect(result.user).toMatchObject({ nickname:'合成用户', gender:2, birth, areaCode:'+86', phoneNumber:'19900000000' });
    expect(result.user.password).toMatch(/^[0-9a-f]{32}$/);
    expect(JSON.stringify(result)).not.toContain('synthetic-password');
    expect(result).not.toHaveProperty('password');
    expect(result).not.toHaveProperty('verifyCode');
    expect(result.autoLogin).toBe(false);
  });
  it('shows only device names, handles duplicate platforms, and distinguishes an empty online list', () => {
    expect(onlineDeviceLabel([])).toBe('离线');
    expect(onlineDeviceLabel(undefined)).toBe('离线');
    expect(onlineDeviceLabel([{platform:'Web',total:2},{platform:'OSX',total:1},{platform:'Web',total:1}])).toBe('Web/OSX在线');
  });
  it('updates only changed profile fields without forwarding credentials, identities or table metadata', () => {
    const original = {nickname:'原昵称', gender:2, birth:0, level:1};
    const edited = {...original, nickname:'新昵称', phoneNumber:'synthetic', password:'do-not-forward',
      email:'private@fixture.example', appMangerLevel:100, onlineStr:'Web在线'};
    expect(changedUserProfile('owner', edited, original)).toEqual({userID:'owner',nickname:'新昵称'});
    expect(changedUserProfile('owner', original, original)).toEqual({userID:'owner'});
    expect(changedUserProfile('owner', {gender:0,birth:0,level:100}, {...original,birth:123})).toEqual({userID:'owner',gender:0,birth:0,level:100});
  });
  it('does not turn missing end times into decades of call duration or wrap after24hours', () => {
    expect(formatRecordDuration(100,0)).toBe('未结束');
    expect(formatRecordDuration(100,99)).toBe('—');
    expect(formatRecordDuration(100,100)).toBe('00:00:00');
    expect(formatRecordDuration(100,100+90061)).toBe('25:01:01');
  });
});
