import { toast } from 'react-toastify';
import { toastConstants } from '../constants';
import { API } from './api';

export function isAdmin() {
  let user = localStorage.getItem('user');
  if (!user) return false;
  try {
    user = JSON.parse(user);
    return user.role >= 10;
  } catch (error) {
    localStorage.removeItem('user');
    return false;
  }
}

export function isRoot() {
  let user = localStorage.getItem('user');
  if (!user) return false;
  try {
    user = JSON.parse(user);
    return user.role >= 100;
  } catch (error) {
    localStorage.removeItem('user');
    return false;
  }
}

export async function copy(text) {
  let okay = true;
  try {
    if (navigator.clipboard){
      await navigator.clipboard.writeText(text);
    } else {
      var textarea = document.createElement("textarea");

      textarea.value = text;

      textarea.style.opacity = 0;
      textarea.style.position = "absolute";
      textarea.style.left = "-9999px";

      document.body.appendChild(textarea);
      
      var range = document.createRange();
      range.selectNode(textarea);
      window.getSelection().removeAllRanges();
      window.getSelection().addRange(range);
      
      document.execCommand("copy");
      
      document.body.removeChild(textarea);
      window.getSelection().removeAllRanges();
    }
  }
   catch (e) {
    okay = false;
    console.error(e);
  }
  return okay;
}

export function isMobile() {
  return window.innerWidth <= 600;
}

let showErrorOptions = { autoClose: toastConstants.ERROR_TIMEOUT };
let showWarningOptions = { autoClose: toastConstants.WARNING_TIMEOUT };
let showSuccessOptions = { autoClose: toastConstants.SUCCESS_TIMEOUT };
let showInfoOptions = { autoClose: toastConstants.INFO_TIMEOUT };
let showNoticeOptions = { autoClose: false };

if (isMobile()) {
  showErrorOptions.position = 'top-center';
  // showErrorOptions.transition = 'flip';

  showSuccessOptions.position = 'top-center';
  // showSuccessOptions.transition = 'flip';

  showInfoOptions.position = 'top-center';
  // showInfoOptions.transition = 'flip';

  showNoticeOptions.position = 'top-center';
  // showNoticeOptions.transition = 'flip';
}

export function showError(error) {
  console.error(error);
  if (error.message) {
    if (error.name === 'AxiosError') {
      switch (error.response?.status) {
        case 401:
          toast.error('登录已失效，请重新登录。', showErrorOptions);
          break;
        case 429:
          toast.error('错误：请求次数过多，请稍后再试！', showErrorOptions);
          break;
        case 500:
          toast.error('错误：服务器内部错误，请联系管理员！', showErrorOptions);
          break;
        case 405:
          toast.info('本站仅作演示之用，无服务端！');
          break;
        default:
          toast.error('错误：' + error.message, showErrorOptions);
      }
      return;
    }
    toast.error('错误：' + error.message, showErrorOptions);
  } else {
    toast.error('错误：' + error, showErrorOptions);
  }
}

export function showWarning(message) {
  toast.warn(message, showWarningOptions);
}

export function showSuccess(message) {
  toast.success(message, showSuccessOptions);
}

export function showInfo(message) {
  toast.info(message, showInfoOptions);
}

export function showNotice(message) {
  toast.info(message, showNoticeOptions);
}

export function openPage(url) {
  try {
    if (!String(url || '').trim()) return;
    const parsed = new URL(url, window.location.origin);
    if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
      window.open(parsed.href, '_blank', 'noopener,noreferrer');
    }
  } catch (error) {
    console.warn('Ignored an invalid link', error);
  }
}

export function safeHttpURL(value) {
  try {
    if (!String(value || '').trim()) return '';
    const parsed = new URL(value, window.location.origin);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
      ? parsed.href
      : '';
  } catch (error) {
    return '';
  }
}

const SAFE_HTML_TAGS = new Set([
  'A', 'B', 'BLOCKQUOTE', 'BR', 'CODE', 'DEL', 'EM', 'H1', 'H2', 'H3',
  'H4', 'H5', 'H6', 'HR', 'I', 'LI', 'OL', 'P', 'PRE', 'S', 'STRONG',
  'TABLE', 'TBODY', 'TD', 'TH', 'THEAD', 'TR', 'U', 'UL',
]);
const DROP_HTML_TAGS = new Set([
  'IFRAME', 'OBJECT', 'SCRIPT', 'STYLE', 'SVG', 'MATH', 'TEMPLATE',
]);

export function sanitizeHTML(value) {
  const parser = new DOMParser();
  const parsedDocument = parser.parseFromString(String(value || ''), 'text/html');
  const cleanNode = (node) => {
    Array.from(node.children).forEach((child) => {
      const tagName = child.tagName.toUpperCase();
      if (DROP_HTML_TAGS.has(tagName)) {
        child.remove();
        return;
      }
      cleanNode(child);
      if (!SAFE_HTML_TAGS.has(tagName)) {
        child.replaceWith(...Array.from(child.childNodes));
        return;
      }
      Array.from(child.attributes).forEach((attribute) => {
        if (tagName === 'A' && attribute.name === 'href') {
          try {
            const link = new URL(attribute.value, window.location.origin);
            if (!['http:', 'https:', 'mailto:'].includes(link.protocol)) {
              child.removeAttribute(attribute.name);
            } else {
              child.setAttribute(attribute.name, link.href);
            }
          } catch (error) {
            child.removeAttribute(attribute.name);
          }
          return;
        }
        if (
          ['TD', 'TH'].includes(tagName) &&
          ['colspan', 'rowspan'].includes(attribute.name)
        ) {
          return;
        }
        child.removeAttribute(attribute.name);
      });
    });
  };
  cleanNode(parsedDocument.body);
  return parsedDocument.body.innerHTML;
}

export function removeTrailingSlash(url) {
  if (url.endsWith('/')) {
    return url.slice(0, -1);
  } else {
    return url;
  }
}

export function timestamp2string(timestamp) {
  let date = new Date(timestamp * 1000);
  let year = date.getFullYear().toString();
  let month = (date.getMonth() + 1).toString();
  let day = date.getDate().toString();
  let hour = date.getHours().toString();
  let minute = date.getMinutes().toString();
  let second = date.getSeconds().toString();
  if (month.length === 1) {
    month = '0' + month;
  }
  if (day.length === 1) {
    day = '0' + day;
  }
  if (hour.length === 1) {
    hour = '0' + hour;
  }
  if (minute.length === 1) {
    minute = '0' + minute;
  }
  if (second.length === 1) {
    second = '0' + second;
  }
  return (
    year + '-' + month + '-' + day + ' ' + hour + ':' + minute + ':' + second
  );
}

export function downloadTextAsFile(text, filename) {
  let blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  let url = URL.createObjectURL(blob);
  let a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
}

export async function testChannel(username, token, channel) {
  let res = await API.post(`/push/${username}/`, {
    token,
    channel,
    title: 'NotiHarbor 讯港',
    description:
      channel === ''
        ? '消息推送通道测试成功'
        : `消息推送通道 ${channel} 测试成功`,
    content: '欢迎使用 NotiHarbor 讯港，这是一条测试消息。',
  });
  const { success, message } = res.data;
  if (success) {
    showSuccess('测试消息已发送');
  } else {
    showError(message);
  }
}

export const verifyJSON = (str) => {
  try {
    JSON.parse(str);
  } catch (e) {
    return false;
  }
  return true;
};

export const generateToken = (byteNum) => {
  const bytes = crypto.getRandomValues(new Uint8Array(byteNum));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0'))
    .join('')
    .toUpperCase();
};
