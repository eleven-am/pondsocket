use std::sync::Arc;

use crate::channel::Channel;
use crate::types::User;

pub struct LeaveContext {
    pub channel: Arc<Channel>,
    pub user: User,
    pub reason: String,
}
