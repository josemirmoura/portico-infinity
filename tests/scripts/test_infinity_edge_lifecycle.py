"""VPS edge lifecycle owner and foreign-resource regressions.

All Docker actions in these unit tests are fake and non-destructive.
"""
import importlib.util
import json
from pathlib import Path
import unittest

SOURCE=Path(__file__).resolve().parents[2]/"deploy/infinity-vps-edge/infinity-edge-lifecycle.py"
SPEC=importlib.util.spec_from_file_location("infinity_edge_lifecycle",SOURCE)
MOD=importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)
A="a"*64
B="b"*64


class FakeDocker:
    def __init__(self):
        self.bridge={A:{"running":True,"restart":"unless-stopped"}}
        self.foreign={B:{"running":True,"restart":"unless-stopped"}}
        self.calls=[]
        self.bad_label=False
        self.bad_path=False
        self.bad_mount=False
        self.bad_network=False
        self.unexpected_volume=False
        self.owned_network=False
        self.new_bridge_on_second_poll=False
        self.polls=0

    def run(self,args):
        self.calls.append(tuple(args))
        if args[:3]==["docker","network","ls"]:
            return B+"\n" if self.owned_network else ""
        if args[:3]==["docker","volume","ls"]:
            return "foreign_vol\n" if self.unexpected_volume else ""
        if args[:2]==["docker","ps"]:
            self.polls+=1
            if self.new_bridge_on_second_poll and self.polls>=2:
                return A+"\n"+B+"\n"
            return "\n".join(self.bridge)+"\n" if self.bridge else ""
        if args[:2]==["docker","inspect"]:
            ident=args[2]
            if ident!=A:
                raise AssertionError("foreign container cannot be inspected as owned")
            row=self.bridge[ident]
            data={
              "Id":ident,
              "Config":{"Labels":{
                "com.docker.compose.project":"another-project" if self.bad_label else MOD.PROJECT,
                "com.docker.compose.service":"bridge",
                "com.docker.compose.project.working_dir":"/srv/foreign" if self.bad_path else str(MOD.ROOT),
                "traefik.enable":"true",
              }},
              "State":{"Running":row["running"]},
              "HostConfig":{"PortBindings":{},"RestartPolicy":{"Name":row["restart"]}},
              "NetworkSettings":{"Networks":{"other-network":{}}
                  if self.bad_network else {MOD.SHARED_NETWORK:{}}},
              "Mounts":[{
                "Type":"bind","Source":"/etc/shadow" if self.bad_mount else str(MOD.CONFIG),
                "Destination":"/etc/traefik/infinity-upstreams.yml","RW":False,
              }],
            }
            return json.dumps([data])
        if args[:2]==["docker","update"]:
            assert args[2]=="--restart=no"
            self.bridge[args[-1]]["restart"]="no"
            return args[-1]
        if args[:2]==["docker","stop"]:
            self.bridge[args[-1]]["running"]=False
            return args[-1]
        if args[:2]==["docker","rm"]:
            del self.bridge[args[-1]]
            return args[-1]
        raise AssertionError("unexpected external call: "+str(args))

    def mutations(self):
        return [x for x in self.calls if x[:2] in (
            ("docker","update"),("docker","stop"),("docker","rm"),
            ("docker","network"),("docker","volume")) and x[:3] not in (
            ("docker","network","ls"),("docker","volume","ls"))]


class EdgeLifecycleTests(unittest.TestCase):
    def test_deactivate_then_remove_preserves_shared_and_foreign(self):
        fake=FakeDocker()
        self.assertEqual(MOD.change("deactivate",fake.run),(1,0))
        self.assertFalse(fake.bridge[A]["running"])
        self.assertEqual(fake.bridge[A]["restart"],"no")
        self.assertTrue(fake.foreign[B]["running"])
        self.assertEqual(MOD.change("remove",fake.run),(1,1))
        self.assertEqual(MOD.inventory(fake.run),[])
        self.assertTrue(fake.foreign[B]["running"])
        self.assertFalse(any(x[1] in ("network","volume") and x[2]=="rm"
                             for x in fake.calls))

    def test_empty_project_idempotent(self):
        fake=FakeDocker()
        fake.bridge={}
        self.assertEqual(MOD.change("deactivate",fake.run),(0,0))
        self.assertEqual(MOD.change("remove",fake.run),(0,0))
        self.assertEqual(fake.mutations(),[])

    def test_unowned_identity_and_mounts_fail_before_mutation(self):
        for attr in ("bad_label","bad_path","bad_mount","bad_network",
                     "unexpected_volume","owned_network"):
            with self.subTest(attr=attr):
                fake=FakeDocker()
                setattr(fake,attr,True)
                with self.assertRaises(MOD.EdgeBlocked):
                    MOD.change("remove",fake.run)
                self.assertEqual(fake.mutations(),[])

    def test_second_inventory_race_blocks_before_mutation(self):
        fake=FakeDocker()
        fake.new_bridge_on_second_poll=True
        with self.assertRaises(MOD.EdgeBlocked):
            MOD.change("remove",fake.run)
        self.assertEqual(fake.mutations(),[])

    def test_invalid_duplicate_docker_ids_fail_closed(self):
        for raw in ("a"*10+"\n","a"*64+"\n"+"a"*64+"\n","../tmp\n"):
            with self.assertRaises(MOD.EdgeBlocked):
                MOD.ids(raw)

    def test_no_external_actions_are_exposed(self):
        code=SOURCE.read_text()
        for unsafe in ("docker network rm","docker volume rm","docker system prune",
                       "wg-quick down","ufw delete","systemctl stop traefik","rm -rf /opt"):
            self.assertNotIn(unsafe,code)


if __name__=="__main__":
    unittest.main()
