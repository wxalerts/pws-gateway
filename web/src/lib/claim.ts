/** The WxAlerts page where the code is entered. Used when the gateway reports none. */
export const CLAIM_PAGE = 'https://wxalerts.org/pws/claim';
/** Where a linked gateway shows up. */
export const WXALERTS_DASHBOARD = 'https://wxalerts.org/pws';

/** The code read out letter by letter, for the accessible name. */
export function spokenCode(code: string): string {
  return code.replace('-', '').split('').join(' ');
}
