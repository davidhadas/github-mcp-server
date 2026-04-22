
# Kagenti MCP Elicitation URL Mode 

## Overview

This document describes the design and implementation of MCP Elicitation URL Mode in the AuthBridge sidecar which is using envoy technology. The goal is to enable secure, token‑based communication between an AI Agent and an MCP server using a URL‑based authorization flow. Tokens and the MCP Elicitation URL Mode protocol are handled entirely by the Kagenti system and are not visible to the AI Agent. See the [MCP Elicitation URL Mode specification](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation/) for additional background.

This POC will be enhancing the AuthBridge sidecar at ./kagenti-extensions. The AUthBridge sidecar support different types of mcp server and we now add a new type of MCP server: server that supports the MCP Elicitation URL Mode.

The Kagenti system uses a Token Broker, which is responsible for managing the token lifecycle and ensuring secure communication between the AI Agent and the MCP server. The Token Broker initiates the OAuth flow when needed and acts as the OAuth client, including full support for PKCE.

The Token Broker caches tokens. A token is maintained for eachcombiantion of a user (given the user id in the kagenti system) and an MCP server (given the MCP server base URL or any other MCP server unique id in the kagenti system). If a required token is not in the cache, the Token Broker sends a synthetic MCP request to the MCP server without a token. The MCP server responds with a 401 Unauthorized containing an elicitation URL. The Token Broker then performs MCP server discovery and sends a request to the Backend that includes the elicitation URL.

The Backend redirects the user to the elicitation URL for login and authorization. After the user completes login, the Backend sends the resulting authorization code and state back to the Token Broker as a new events request. The Token Broker exchanges the authorization code for a token (using the internally stored PKCE code_verifier), caches the token, and then returns the token to AuthBridge, which ultimately forwards it to the Agent.

See for more details on the MCP Elicitation URL Mode. This document maintains the design of an MCP Elicitation URL Mode implemented in an AuthBridge sidecar serving an ai agent running on a k8s cluster. The Agent is not aware of the  MCP Elicitation URL Mode process and see no tokens. The token broker is running as a service on the k8s cluster.  

This document describes how MCP Elicitation URL Mode is implemented in an AuthBridge sidecar architecture serving an AI Agent running in a Kubernetes cluster. The Agent is not aware of the elicitation process and never sees tokens. All token handling is performed by the Kagenti system, which includes a Token Broker running as a shared service in the cluster.

## Components

- **Frontend** – user-facing UI  
- **Backend** – application server used by the users  
- **Agent** – An LLM-based AI Agent
- **AuthBridge** – sidecar proxy next to the Agent
- **MCP Server** – tool endpoint supporting Elicitation URL Mode  
- **Token Broker** – service responsible for obtaining, caching, and managing tokens
- **OAuth Provider** – external identity provider (e.g., GitHub)

## System Characteristics
- There are multiple users. Each user may run multiple Agents, and each Agent may perform different tasks.
- Agents may call other Agents and may connect to multiple MCP servers.
- MCP servers require user‑scoped tokens. Different MCP servers may require different tokens, and different users accessing the same MCP server will use different tokens. Each MCP server is associated with an OAuth provider.
- The Token Broker is a shared Kagenti service.

## Sessions and the oauth_session_key
When a user connects to an Agent through the Backend, if the backend supports the oauth flow, it will first establish a session with the Token Broker by calling POST /sessions. The Token Broker verify the user_id (carried as part of the signed kagenti user access token) and returns the oauth_session_key to the backend. The Token Broker records that the oauth_session_key is associated with the user_id. Anyone approaching the /sessions/{oauth_session_key}/* must here on have a matching user access token for the same user_id. Future design changes may include enbedding the oauth_session_key as a token claim of the kagenti user access token.

The backend will from now on include the oauth_session_key as a header when sending reqeusts to the Agent. The backend will also include the oauth_session_key when connecting to the Token Broker hence forward using the POST /sessions/{oauth_session_key}/events and until it terminates the session by calling POST /sessions/{oauth_session_key}/end. If an Agent calls another Agent, it forwards the same oauth_session_key header taht as used to call him (if exists). When an agent sends a reqeust to an MCP server, it includes the oauth_session_key in the request headers. When AuthBridge requests a token from the Token Broker on behalf of an Agent, the AuthBridge calls POST /sessions/{oauth_session_key}/token of the Token Broker. The oauth_session_key is therefore used to associate all Token Broker interactions with the correct user and user session and to ensure that the Token Broker can correlate Backend events requests with AuthBridge token requests. 

At any given time, the Token Broker may receive multiple token requests from AuthBridge instances associated with the same oauth_session_key. If a token is missing for that user and mcp server combination, the Token Broker must obtain it using OAuth flow described here with the help of the user. To prevent multiple concurrent OAuth flows for the same oauth_session_key, the Token Broker maintains a session semaphore ensuring that only one token acquisition is in progress per session.

When AuthBridge asks the Token Broker for a token, the Token Broker performs the following steps:
1. **Check Cache:** The Token Broker checks whether a token is already cached for the given user and MCP server. If so, it returns the token immediately.
2. **Wait on Semaphore:** If the token is not cached, the Token Broker acquires the session semaphore for that oauth_session_key.
3. **Check Cache Again:** After acquiring the semaphore, the Token Broker checks the cache again. Another request may have already obtained and cached the token. If the token is now present, it is returned immediately and the semaphore is released.
4. **Obtain Token:** If the token is still not cached, the Token Broker initiates the OAuth flow and obtains the token with the user’s help.
5. **Cache Token:** The Token Broker caches the token for the given user and MCP server combination.
6. **Release Semaphore:** The Token Broker releases the session semaphore and returns the token.

When the Backend ends the session using the /sessions/{oauth_session_key}/end endpointthe Token Broker will expire the session. 
The Token Broker will implement a timer per session that will start anytime a Backend is not waiting on events (performs long-polling), once the timer expires the Token Broker will expire the session. The timer will start if either, a response was sent to the previous events request or the backend connection waiting for a response was dropped. The timer will stop as soon as the backend is sending a new events request and is waiting for a response.
When the Token Broker expires a session, it will fail all pending requests of the session with an error, and realease any related resources. The oauth_session_key will be removed from the Token Broker's session cache and any request to /sessions/{oauth_session_key}/* will result in an error.

If the Backend terminates the session or timeout occurs since the end of the last backend tcp session to the token broker, the Token Broker will expire the session, fail all pending requests of the session with an error, and realease any related resources. Any AuthBridge token requests to a non existing or termianted session will fail with an error. The backed is responsiable to maintain an ongoing request to /sessions/{oauth_session_key}/events continuesly for as long as the session need to be maintained and retry any failed tcp connection abruptly terminated. The Token Broker will limit the number of sessions related to a given user and will refuse additional session creations.



## Comments
If during an mcp reqeust by the agent, AuthBridge is unable to obtain a token from the Token Broker, it will return an MCP authentication_failed error response indicating "Failed to obtain user authorization for this MCP server." to the agent.

- User tokens are cached per (user_id, mcp_server) and are not tied to any specific session.

- All erros are sent as json errors.

- The agent is never modified and never sees 401s or auth URLs.

- Agent’s heavy reasoning happens once and need not repeat every time a token is missing while processing the task lineray.

- AuthBridge is used for routing and transport; Token broker holds the stateful logic.

- Cached tokens has expiry time embedded as part of the token. If the token is expired, or near expiary (less than 5 min) the agent will remove the token from cache and hadndle it as a case where token is not cached.

## Future enhancements, not yet covered by this design: 
- Allow a user to remove all the user tokens per user_id.
- If an AuthBridge obtained an access token from the Token Broker, and used it in an mcp request, and the MCP server responds that the token is not valid, AuthBridge will ask the Token Broker to remove the token from cache.
