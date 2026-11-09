export enum AlertType {
  ERROR = 'warn',
  IDLE = 'idle',
  INFO = 'accent',
  SUCCESS = 'success',
}

/** A button on a notice: its label and what a click runs, such as the Reload of an update notice. */
export interface NotificationAction {
  label: string;
  run: () => void;
}

export interface CreateNotificationMessage {
  duration?: number;
  message: string;
  link: string;
  type: AlertType;
  /** A button rendered beside the close; absent on every notice that has none. */
  action?: NotificationAction;
  /** A persistent notice is never dismissed by the alert's timer: it stays until it is closed or dismissed by its id. */
  persistent?: boolean;
}

export interface NotificationMessage {
  id: number;
  duration?: number;
  message: string;
  link: string;
  type: AlertType;
  /** A button rendered beside the close; absent on every notice that has none. */
  action?: NotificationAction;
  /** A persistent notice is never dismissed by the alert's timer: it stays until it is closed or dismissed by its id. */
  persistent?: boolean;
}
