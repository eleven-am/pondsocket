use std::sync::Arc;

use serde_json::Value;

use crate::channel::Channel;
use crate::transport::Transport;
use crate::types::{Event, Route, User};

pub struct OutgoingContext {
    pub channel: Arc<Channel>,
    pub event: Event,
    pub user: User,
    pub transport: Arc<dyn Transport>,
    pub route: Route,
    blocked: bool,
    transformed: bool,
}

impl OutgoingContext {
    pub fn new(
        channel: Arc<Channel>,
        event: Event,
        user: User,
        transport: Arc<dyn Transport>,
    ) -> Self {
        Self {
            channel,
            event,
            user,
            transport,
            route: Route::default(),
            blocked: false,
            transformed: false,
        }
    }

    pub fn block(&mut self) {
        self.blocked = true;
    }

    pub fn unblock(&mut self) {
        self.blocked = false;
    }

    pub fn is_blocked(&self) -> bool {
        self.blocked
    }

    pub fn transform(&mut self, payload: Value) {
        self.event.payload = payload;
        self.transformed = true;
    }

    pub fn has_transformed(&self) -> bool {
        self.transformed
    }
}
