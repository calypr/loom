export const verificationTimeoutMs = process.env.CI === 'true' || process.env.CI === '1' ? 600_000 : 30_000;
export const goTestTimeout = `${verificationTimeoutMs / 1000}s`;
