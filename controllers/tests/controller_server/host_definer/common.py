import unittest
import faulthandler
import signal
from mock import patch, Mock
from kubernetes.client.rest import ApiException

import controllers.tests.controller_server.host_definer.utils.test_utils as test_utils
import controllers.tests.controller_server.host_definer.settings as test_settings
import controllers.tests.common.test_settings as controller_test_settings
import controllers.servers.host_definer.settings as hd_settings


class BaseSetUp(unittest.TestCase):
    def setUp(self):
        test_utils.patch_kubernetes_manager_init()

        self._mock_core_v1 = patch("controllers.servers.host_definer.kubernetes_manager.manager.client.CoreV1Api")
        self.mock_core_v1_cls = self._mock_core_v1.start()

        mock_core_api = Mock()
        mock_core_api.read_node.return_value = Mock(
            metadata=Mock(
                annotations={controller_test_settings.NODE_INITIATORS_FIELD:
                             controller_test_settings.EMPTY_INITIATORS_STR}
                ))

        self.mock_core_v1_cls.return_value = mock_core_api

        self.os = patch(f"{test_settings.WATCHER_HELPER_PATH}.os").start()
        self.nodes_on_watcher_helper = test_utils.patch_nodes_global_variable(test_settings.WATCHER_HELPER_PATH)
        self.managed_secrets_on_watcher_helper = test_utils.patch_managed_secrets_global_variable(
            test_settings.WATCHER_HELPER_PATH)
        self.k8s_node_with_manage_node_label = test_utils.get_fake_k8s_node(test_settings.MANAGE_NODE_LABEL)
        self.k8s_node_with_fake_label = test_utils.get_fake_k8s_node(test_settings.FAKE_LABEL)
        self.ready_k8s_host_definitions = test_utils.get_fake_k8s_host_definitions_items(test_settings.READY_PHASE)
        self.http_resp = test_utils.get_error_http_resp()
        self.fake_api_exception = ApiException(http_resp=self.http_resp)

        try:
            faulthandler.register(signal.SIGUSR2)
        except Exception:
            pass

        hd_settings.HOST_DEFINITION_PENDING_RETRIES = 1
        hd_settings.HOST_DEFINITION_PENDING_EXPONENTIAL_BACKOFF_IN_SECONDS = 1
        hd_settings.HOST_DEFINITION_PENDING_DELAY_IN_SECONDS = 0

        thread_target = "controllers.servers.host_definer.watcher.host_definition_watcher.Thread"
        sleep_target = "controllers.servers.host_definer.watcher.host_definition_watcher.sleep"

        class ImmediateThread:
            def __init__(self, target=None, args=(), kwargs=None, **_):
                self._target = target
                self._args = args
                self._kwargs = kwargs or {}
                self.daemon = True  # keep consistent with tests not blocking exit

            def start(self):
                if self._target:
                    self._target(*self._args, **self._kwargs)

            def join(self, timeout=None):
                pass

        self._patcher_thread = patch(thread_target, ImmediateThread)
        self._patcher_sleep = patch(sleep_target, lambda s: None)

        self._patcher_thread.start()
        self._patcher_sleep.start()

    def tearDown(self):
        patch.stopall()
