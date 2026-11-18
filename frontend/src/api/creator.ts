import api from './client'

export const verifyUser = (uid: string, verified: boolean) =>
  api.put(`/v1/creator/verify/${uid}`, { verified })
