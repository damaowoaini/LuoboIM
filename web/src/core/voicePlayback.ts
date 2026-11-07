/** One voice message owns playback in this Web window. Account/history access
 * remains the component's authority; a lease is never an access permit. */
export type VoicePlaybackLease = {
  current(): boolean;
  release(): void;
};
type ActiveVoice = { audio: HTMLAudioElement; retire(): void };
let active: ActiveVoice | undefined;

export function claimVoicePlayback(audio: HTMLAudioElement, retire: () => void): VoicePlaybackLease {
  const previous = active;
  const entry: ActiveVoice = { audio, retire };
  active = entry;
  // Publish the new identity before retiring A, so A's release cannot clear B.
  // B's caller starts play only after A has synchronously stopped here.
  previous?.retire();
  return {
    current: () => active === entry && active.audio === audio,
    release: () => { if (active === entry && active.audio === audio) active = undefined; },
  };
}
