use pondsocket_common::PondAssigns;
use serde_json::Value;
use std::collections::HashMap;

use crate::errors::{Result, bad_request};
use crate::types::Route;

#[derive(Debug, Clone, Default)]
pub struct IncomingConnection {
    pub id: String,
    pub headers: HashMap<String, String>,
    pub cookies: HashMap<String, String>,
    pub query: HashMap<String, String>,
    pub params: HashMap<String, String>,
    pub address: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnectionDecision {
    Pending,
    Accepted,
    Declined,
}

#[derive(Debug, Clone)]
pub struct ConnectionContext {
    pub user_id: String,
    pub request: IncomingConnection,
    pub route: Route,
    assigns: PondAssigns,
    decision: ConnectionDecision,
    decline_code: u16,
    decline_message: String,
    pending_reply: Option<(String, Value)>,
}

impl ConnectionContext {
    pub fn new(user_id: impl Into<String>, request: IncomingConnection, route: Route) -> Self {
        Self {
            user_id: user_id.into(),
            request,
            route,
            assigns: PondAssigns::new(),
            decision: ConnectionDecision::Pending,
            decline_code: 0,
            decline_message: String::new(),
            pending_reply: None,
        }
    }

    pub fn accept(&mut self, assigns: PondAssigns) -> Result<&mut Self> {
        self.ensure_pending()?;
        self.decision = ConnectionDecision::Accepted;
        self.assigns.extend(assigns);
        Ok(self)
    }

    pub fn decline(&mut self, code: u16, message: impl Into<String>) -> Result<()> {
        self.ensure_pending()?;
        self.decision = ConnectionDecision::Declined;
        self.decline_code = code;
        self.decline_message = message.into();
        Ok(())
    }

    pub fn reply(&mut self, event: impl Into<String>, payload: Value) -> Result<&mut Self> {
        if self.decision == ConnectionDecision::Pending {
            self.decision = ConnectionDecision::Accepted;
        }
        if self.decision == ConnectionDecision::Declined {
            return Err(bad_request("GATEWAY", "cannot reply after declining"));
        }
        self.pending_reply = Some((event.into(), payload));
        Ok(self)
    }

    pub fn set_assign(&mut self, key: impl Into<String>, value: Value) -> &mut Self {
        self.assigns.insert(key.into(), value);
        self
    }

    pub fn decision(&self) -> ConnectionDecision {
        self.decision
    }

    pub fn is_accepted(&self) -> bool {
        self.decision == ConnectionDecision::Accepted
    }

    pub fn is_declined(&self) -> bool {
        self.decision == ConnectionDecision::Declined
    }

    pub fn decline_info(&self) -> (u16, String) {
        (self.decline_code, self.decline_message.clone())
    }

    pub fn assigns(&self) -> PondAssigns {
        self.assigns.clone()
    }

    pub fn pending_reply(&self) -> Option<(String, Value)> {
        self.pending_reply.clone()
    }

    fn ensure_pending(&self) -> Result<()> {
        if self.decision != ConnectionDecision::Pending {
            return Err(bad_request("GATEWAY", "connection response already sent"));
        }
        Ok(())
    }
}
